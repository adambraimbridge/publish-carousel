package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Financial-Times/publish-carousel/blacklist"
	"github.com/Financial-Times/publish-carousel/native"
	"github.com/Financial-Times/publish-carousel/tasks"
	log "github.com/Sirupsen/logrus"
)

type Cycle interface {
	ID() string
	Start()
	Stop()
	Reset()
	Metadata() CycleMetadata
	SetMetadata(state CycleMetadata)
	TransformToConfig() *CycleConfig
	State() []string
}

type CycleMetadata struct {
	CurrentPublishUUID  string     `json:"currentPublishUuid"`
	CurrentPublishRef   string     `json:"currentPublishReference"`
	CurrentPublishError string     `json:"currentPublishError,omitempty"`
	Errors              int        `json:"errors"`
	Progress            float64    `json:"progress"`
	State               []string   `json:"state"`
	Completed           int        `json:"completed"`
	Total               int        `json:"total"`
	Iteration           int        `json:"iteration"`
	Attempts            int        `json:"attempts"`
	Start               *time.Time `json:"windowStart,omitempty"`
	End                 *time.Time `json:"windowEnd,omitempty"`

	state map[string]struct{}
}

func newCycleID(name string, dbcollection string) string {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte(dbcollection))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func newAbstractCycle(name string, cycleType string, blist blacklist.IsBlacklisted, database native.DB, dbCollection string, origin string, coolDown time.Duration, task tasks.Task) *abstractCycle {
	cycle := &abstractCycle{
		CycleID:       newCycleID(name, dbCollection),
		Name:          name,
		Type:          cycleType,
		CycleMetadata: CycleMetadata{state: make(map[string]struct{})},
		metadataLock:  &sync.RWMutex{},
		db:            database,
		DBCollection:  dbCollection,
		Origin:        origin,
		CoolDown:      coolDown.String(),
		coolDown:      coolDown,
		publishTask:   task,
		blacklist:     blist,
	}
	cycle.UpdateState(stoppedState)

	return cycle
}

type abstractCycle struct {
	CycleID       string        `json:"id"`
	Name          string        `json:"name"`
	Type          string        `json:"type"`
	CycleMetadata CycleMetadata `json:"metadata"`
	DBCollection  string        `json:"collection"`
	Origin        string        `json:"origin"`
	CoolDown      string        `json:"coolDown"`

	coolDown     time.Duration
	metadataLock *sync.RWMutex
	cancel       context.CancelFunc
	db           native.DB
	publishTask  tasks.Task
	blacklist    blacklist.IsBlacklisted
}

func (a *abstractCycle) publishCollection(ctx context.Context, collection native.UUIDCollection, t Throttle) (bool, error) {
	for {
		t.Queue()

		if err := ctx.Err(); err != nil {
			return true, err
		}

		finished, uuid, err := collection.Next()
		if finished {
			log.WithField("id", a.CycleID).WithField("name", a.Name).WithField("collection", a.DBCollection).Info("Finished publishing collection.")
			a.updateProgress("", "", err)
			return false, err
		}

		if strings.TrimSpace(uuid) == "" { // N.B. UUID cannot be empty for the in memory collection
			log.WithField("id", a.CycleID).WithField("name", a.Name).WithField("collection", a.DBCollection).Warn("Next UUID is empty! Skipping.")
			a.updateProgress(uuid, "", errors.New("Empty uuid"))
			continue
		}

		log.WithField("id", a.CycleID).WithField("name", a.Name).WithField("collection", a.DBCollection).WithField("uuid", uuid).Info("Running publish task.")
		content, txID, err := a.publishTask.Prepare(a.DBCollection, uuid)

		if err == nil {
			err = a.publishTask.Execute(uuid, content, a.Origin, txID)
			if err != nil {
				log.WithField("id", a.CycleID).WithField("name", a.Name).WithField("collection", a.DBCollection).WithField("uuid", uuid).WithError(err).Warn("Failed to publish!")
			}
		}

		a.updateProgress(uuid, txID, err)
	}
}

func (a *abstractCycle) updateProgress(uuid string, txId string, err error) {
	a.metadataLock.Lock()
	defer a.metadataLock.Unlock()

	if err == nil {
		a.CycleMetadata.CurrentPublishError = ""
	} else {
		a.CycleMetadata.Errors++
		a.CycleMetadata.CurrentPublishError = err.Error()
	}

	a.CycleMetadata.Completed++
	a.CycleMetadata.CurrentPublishUUID = uuid
	a.CycleMetadata.CurrentPublishRef = txId

	if a.CycleMetadata.Total == 0 {
		a.CycleMetadata.Progress = 0
	} else {
		a.CycleMetadata.Progress = float64(a.CycleMetadata.Completed) / float64(a.CycleMetadata.Total)
	}
}

func (a *abstractCycle) ID() string {
	return a.CycleID
}

func (a *abstractCycle) Stop() {
	a.cancel()
	log.WithField("id", a.CycleID).WithField("name", a.Name).WithField("collection", a.DBCollection).Info("Cycle stopped.")
	a.UpdateState(stoppedState)
}

func (a *abstractCycle) Reset() {
	a.Stop()
	metadata := CycleMetadata{state: make(map[string]struct{})}
	a.SetMetadata(metadata)
}

func (a *abstractCycle) Metadata() CycleMetadata {
	a.metadataLock.Lock()
	defer a.metadataLock.Unlock()

	return a.CycleMetadata
}

func (a *abstractCycle) SetMetadata(metadata CycleMetadata) {
	a.metadataLock.Lock()
	defer a.metadataLock.Unlock()

	if metadata.state == nil {
		metadata.state = make(map[string]struct{})
	}

	a.CycleMetadata = metadata
}

func (a *abstractCycle) UpdateState(states ...string) {
	a.metadataLock.Lock()
	defer a.metadataLock.Unlock()

	a.CycleMetadata.state = make(map[string]struct{})

	for _, state := range states {
		a.CycleMetadata.state[state] = struct{}{}
	}

	var arr []string
	for k := range a.CycleMetadata.state {
		arr = append(arr, k)
	}

	sort.Strings(arr)
	a.CycleMetadata.State = arr
}

func (a *abstractCycle) PublishedItems() int {
	a.metadataLock.RLock()
	defer a.metadataLock.RUnlock()
	return a.CycleMetadata.Completed
}

func (a *abstractCycle) State() []string {
	a.metadataLock.RLock()
	defer a.metadataLock.RUnlock()
	return a.CycleMetadata.State
}
