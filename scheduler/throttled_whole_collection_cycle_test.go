package scheduler

import (
	"errors"
	"testing"
	"time"

	"gopkg.in/mgo.v2/bson"

	"github.com/Financial-Times/publish-carousel/native"
	"github.com/Financial-Times/publish-carousel/tasks"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func mockDB(opened chan struct{}, tx native.TX, err error) *native.MockDB {
	db := new(native.MockDB)
	db.On("Open").Return(tx, err).Run(func(arg1 mock.Arguments) {
		opened <- struct{}{}
	})
	return db
}

func mockTx(iter native.DBIter, expectedSkip int, err error) *native.MockTX {
	mockTx := new(native.MockTX)
	mockTx.On("FindUUIDs", "collection", expectedSkip, 80).Return(iter, 15, err)
	return mockTx
}

func mockIter(expectedUUID string, moreItems bool, next chan struct{}, closed chan struct{}) *native.MockDBIter {
	iter := new(native.MockDBIter)
	iter.On("Next", mock.MatchedBy(func(arg *map[string]interface{}) bool {
		m := *arg
		m["uuid"] = bson.Binary{Kind: 0x04, Data: []byte(uuid.Parse(expectedUUID))}
		return true
	})).Run(func(arg1 mock.Arguments) {
		next <- struct{}{}
	}).Return(moreItems)

	iter.On("Close").Run(func(arg1 mock.Arguments) {
		closed <- struct{}{}
	}).Return(nil)

	return iter
}

func mockThrottle(interval time.Duration, called chan struct{}) *MockThrottle {
	throttle := new(MockThrottle)
	throttle.On("Interval").Return(interval)
	throttle.On("Queue").Run(func(arg1 mock.Arguments) {
		time.Sleep(interval)
		called <- struct{}{}
	}).Return(nil)
	return throttle
}

func mockTask(expectedUUID string, err error) *tasks.MockTask {
	task := new(tasks.MockTask)
	task.On("Publish", "origin", "collection", expectedUUID).Return(err)
	return task
}

func TestWholeCollectionCycleRunWithMetadata(t *testing.T) {
	expectedUUID := uuid.NewUUID().String()
	expectedSkip := 500

	task := mockTask(expectedUUID, nil)

	throttleCalled := make(chan struct{}, 1)
	opened := make(chan struct{}, 1)
	nextCalled := make(chan struct{}, 1)
	closed := make(chan struct{}, 1)

	throttle := mockThrottle(time.Millisecond*50, throttleCalled)

	iter := mockIter(expectedUUID, true, nextCalled, closed)
	iter.On("Timeout").Return(false)

	tx := mockTx(iter, expectedSkip, nil)
	db := mockDB(opened, tx, nil)

	cycle := NewThrottledWholeCollectionCycle("name", db, "collection", "origin", time.Millisecond*50, throttle, task)

	metadata := CycleMetadata{Completed: expectedSkip, Iteration: 1}
	cycle.SetMetadata(metadata)

	cycle.Start()

	assert.Len(t, cycle.State(), 1)
	assert.Contains(t, cycle.State(), startingState)

	<-opened
	<-throttleCalled

	assert.Len(t, cycle.State(), 1)
	assert.Contains(t, cycle.State(), runningState)

	<-nextCalled

	cycle.Stop()

	<-throttleCalled
	<-closed

	assert.Len(t, cycle.State(), 1)
	assert.Contains(t, cycle.State(), stoppedState)

	mock.AssertExpectationsForObjects(t, throttle, iter, tx, db, task)

	assert.Equal(t, 1, cycle.Metadata().Iteration)
	assert.Equal(t, 501, cycle.Metadata().Completed)
}

func TestWholeCollectionCycleTaskFails(t *testing.T) {
	expectedUUID := uuid.NewUUID().String()
	expectedSkip := 0

	task := mockTask(expectedUUID, errors.New("i fail soz"))

	throttleCalled := make(chan struct{}, 1)
	opened := make(chan struct{}, 1)
	nextCalled := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)

	throttle := mockThrottle(time.Millisecond*50, throttleCalled)

	iter := mockIter(expectedUUID, true, nextCalled, stopped)
	iter.On("Timeout").Return(false)

	tx := mockTx(iter, expectedSkip, nil)
	db := mockDB(opened, tx, nil)

	c := NewThrottledWholeCollectionCycle("name", db, "collection", "origin", time.Millisecond*50, throttle, task)

	c.Start()

	<-opened
	<-throttleCalled
	<-nextCalled

	c.Stop()

	<-throttleCalled
	<-stopped

	assert.Len(t, c.State(), 1)
	assert.Contains(t, c.State(), stoppedState)

	mock.AssertExpectationsForObjects(t, throttle, iter, tx, db, task)
	assert.Equal(t, 1, c.Metadata().Errors)
}

func TestWholeCollectionCycleRunCompleted(t *testing.T) {
	expectedUUID := uuid.NewUUID().String()
	expectedSkip := 0

	task := new(tasks.MockTask)

	throttleCalled := make(chan struct{}, 1)
	opened := make(chan struct{}, 1)
	nextCalled := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)

	throttle := mockThrottle(time.Millisecond*50, throttleCalled)

	count := 0
	iter := new(native.MockDBIter)

	iter.On("Next", mock.MatchedBy(func(arg *map[string]interface{}) bool {
		m := *arg
		m["uuid"] = bson.Binary{Kind: 0x04, Data: []byte(uuid.Parse(expectedUUID))}

		if count < 3 {
			count++
			nextCalled <- struct{}{}
			return true
		}
		return false
	})).Return(false)

	iter.On("Next", mock.MatchedBy(func(arg *map[string]interface{}) bool {
		m := *arg
		m["uuid"] = bson.Binary{Kind: 0x04, Data: []byte(uuid.Parse(expectedUUID))}

		if count == 3 {
			count = 0
			nextCalled <- struct{}{}
			return true
		}
		return false
	})).Return(false)

	iter.On("Close").Run(func(arg1 mock.Arguments) {
		stopped <- struct{}{}
	}).Return(nil)

	iter.On("Err").Return(nil)
	iter.On("Timeout").Return(false)

	tx := mockTx(iter, expectedSkip, nil)
	db := mockDB(opened, tx, nil)

	c := NewThrottledWholeCollectionCycle("name", db, "collection", "origin", time.Millisecond*50, throttle, task)

	c.Start()

	<-opened
	<-throttleCalled
	<-nextCalled

	// send another
	<-throttleCalled
	<-nextCalled

	c.Stop()

	<-throttleCalled
	<-stopped

	assert.Len(t, c.State(), 1)
	assert.Contains(t, c.State(), stoppedState)

	mock.AssertExpectationsForObjects(t, throttle, iter, tx, db, task)
	assert.Equal(t, 0, c.Metadata().Errors)
	assert.Equal(t, 2, c.Metadata().Iteration)
}

func TestWholeCollectionCycleIterationError(t *testing.T) {
	expectedUUID := uuid.NewUUID().String()
	expectedSkip := 0

	task := new(tasks.MockTask)

	throttleCalled := make(chan struct{}, 1)
	opened := make(chan struct{}, 1)
	nextCalled := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)

	throttle := mockThrottle(time.Millisecond*50, throttleCalled)

	iter := mockIter(expectedUUID, false, nextCalled, stopped)
	iter.On("Err").Return(errors.New("ruh-roh"))

	tx := mockTx(iter, expectedSkip, nil)
	db := mockDB(opened, tx, nil)

	c := NewThrottledWholeCollectionCycle("name", db, "collection", "origin", time.Millisecond*50, throttle, task)

	c.Start()

	<-opened
	<-throttleCalled
	<-nextCalled

	<-stopped

	assert.Len(t, c.State(), 2)
	assert.Contains(t, c.State(), stoppedState)
	assert.Contains(t, c.State(), unhealthyState)

	mock.AssertExpectationsForObjects(t, throttle, iter, tx, db, task)
}

func TestWholeCollectionCycleRunEmptyUUID(t *testing.T) {
	expectedUUID := ""
	expectedSkip := 0

	task := new(tasks.MockTask)

	throttleCalled := make(chan struct{}, 1)
	opened := make(chan struct{}, 1)
	nextCalled := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)

	throttle := mockThrottle(time.Millisecond*50, throttleCalled)

	iter := mockIter(expectedUUID, true, nextCalled, stopped)
	iter.On("Timeout").Return(false)

	tx := mockTx(iter, expectedSkip, nil)
	db := mockDB(opened, tx, nil)

	c := NewThrottledWholeCollectionCycle("name", db, "collection", "origin", time.Millisecond*50, throttle, task)

	c.Start()

	<-opened
	<-throttleCalled
	<-nextCalled

	c.Stop()

	<-throttleCalled
	<-stopped

	assert.Len(t, c.State(), 1)
	assert.Contains(t, c.State(), stoppedState)

	mock.AssertExpectationsForObjects(t, throttle, iter, tx, db, task)
}

func TestWholeCollectionCycleMongoDBConnectionError(t *testing.T) {
	task := new(tasks.MockTask)

	opened := make(chan struct{}, 1)

	throttle := new(MockThrottle)
	throttle.On("Interval").Return(time.Millisecond * 50)

	tx := new(native.MockTX)
	db := mockDB(opened, tx, errors.New("nein"))

	c := NewThrottledWholeCollectionCycle("name", db, "collection", "origin", time.Millisecond*50, throttle, task)

	c.Start()
	<-opened

	time.Sleep(50 * time.Millisecond)

	assert.Len(t, c.State(), 2)
	assert.Contains(t, c.State(), stoppedState)
	assert.Contains(t, c.State(), unhealthyState)

	mock.AssertExpectationsForObjects(t, throttle, tx, db, task)
}

func TestWholeCollectionCycleRunEmptyCollection(t *testing.T) {
	opened := make(chan struct{}, 1)
	closed := make(chan struct{}, 1)

	iter := new(native.MockDBIter)
	iter.On("Close").Run(func(arg1 mock.Arguments) {
		closed <- struct{}{}
	}).Return(nil)

	tx := new(native.MockTX)
	tx.On("FindUUIDs", "a-collection", 0, 80).Return(iter, 0, nil)

	db := mockDB(opened, tx, nil)

	task := new(tasks.MockTask)
	throttle := new(MockThrottle)
	throttle.On("Interval").Return(1 * time.Second)

	c := NewThrottledWholeCollectionCycle("test-cycle", db, "a-collection", "a-origin-id", 1*time.Second, throttle, task)
	c.Start()

	<-opened
	<-closed

	assert.Len(t, c.State(), 2)
	assert.Contains(t, c.State(), stoppedState)
	assert.Contains(t, c.State(), unhealthyState)

	mock.AssertExpectationsForObjects(t, db, tx, task, throttle)
}

func TestThrottledWholeCollectionTransformToConfig(t *testing.T) {
	db := new(native.MockDB)
	mockTx := new(native.MockTX)
	iter := new(native.MockDBIter)
	task := new(tasks.MockTask)
	throttle := new(MockThrottle)

	throttle.On("Interval").Return(time.Minute)

	c := NewThrottledWholeCollectionCycle("test-cycle", db, "a-collection", "a-origin-id", 1*time.Second, throttle, task)

	conf := c.TransformToConfig()
	assert.Equal(t, "a-collection", conf.Collection)
	assert.Equal(t, "a-origin-id", conf.Origin)
	assert.Equal(t, "test-cycle", conf.Name)
	assert.Equal(t, "ThrottledWholeCollection", conf.Type)
	assert.Equal(t, time.Second.String(), conf.CoolDown)
	assert.Equal(t, time.Minute.String(), conf.Throttle)

	mock.AssertExpectationsForObjects(t, db, mockTx, task, iter, throttle)
}
