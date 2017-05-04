package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/Financial-Times/publish-carousel/etcd"
	log "github.com/Sirupsen/logrus"
)

type externalService struct {
	sync.RWMutex
	name               string
	environmentService *environmentService
}

// NewExternalService returns a new instance of a UPP cluster service which is in an external cluster (i.e. delivery)
func NewExternalService(name string, watcher etcd.Watcher, readURLsKey string) (Service, error) {
	environmentService, err := newEnvironmentService(watcher, readURLsKey)
	environmentService.startWatcher(context.Background())

	return &externalService{name: name, environmentService: environmentService}, err
}

func (e *externalService) Description() string {
	envs := e.environmentService.GetEnvironments()

	url := ""
	for _, env := range envs {
		url += env.name + ": " + env.readURL.String() + ", "
	}
	return url
}

func (e *externalService) Name() string {
	return e.name
}

func (e *externalService) GTG() error {
	e.RLock()
	defer e.RUnlock()

	envs := e.environmentService.GetEnvironments()

	errs := make([]error, 0)
	for _, env := range envs {
		gtg := gtgURLFor(env, e.Name())
		log.WithField("gtg", gtg).Info("Calling GTG for external service.")

		req, err := http.NewRequest("GET", gtg, nil)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		resp, err := http.DefaultClient.Do(req)

		if err != nil {
			log.WithError(err).WithField("service", e.Name()).Error("Failed to call the GTG endpoint of the service")
			errs = append(errs, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("GTG for %v@%v returned a non-200 code: %v", e.Name(), gtg, resp.StatusCode)
			log.WithError(err).Warn("GTG failed for external dependency.")
			errs = append(errs, err)
		}
	}

	return compactErrors("Failure occurred while checking GTG for external service.", errs...)
}

func gtgURLFor(env readEnvironment, name string) string {
	return env.readURL.String() + "/__" + name + "/__gtg"
}

func compactErrors(msg string, errs ...error) error {
	if len(errs) == 0 {
		return nil
	}

	for _, err := range errs {
		msg += "\n" + err.Error()
	}

	return errors.New(msg)
}