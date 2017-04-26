package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Financial-Times/publish-carousel/etcd"
	etcdClient "github.com/coreos/etcd/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var client etcdClient.Client
var api etcdClient.KeysAPI

func init() {
	cfg := etcdClient.Config{
		Endpoints: []string{"http://localhost:2379"},
	}

	var err error
	client, err = etcdClient.New(cfg)
	if err != nil {
		panic(err)
	}

	api = etcdClient.NewKeysAPI(client)
}

func setupTests(t *testing.T, readURLs string, credentials string) etcd.Watcher {
	watcher, err := etcd.NewEtcdWatcher([]string{"http://localhost:2379"})
	assert.NoError(t, err)

	ctx := context.Background()

	assert.NoError(t, err)

	api.Set(ctx, readURLs, "environment:http://localhost:8080", nil)
	api.Set(ctx, credentials, "environment:user:pass", nil)

	return watcher
}

func assertEnvironment(t *testing.T, env readEnvironment, name string, url string, user string, pass string) {
	assert.Equal(t, name, env.name)
	assert.Equal(t, url, env.readURL.String())
	assert.Equal(t, user, env.authUser)
	assert.Equal(t, pass, env.authPassword)
}

func etcdKeys(testID string) (string, string) {
	return "/" + testID + "/ft/config/monitoring/read-urls", "/" + testID + "/ft/_credentials/publish-read/read-credentials"
}

func TestSetupReadCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping etcd integration test")
	}

	readURLsKey, credentialsKey := etcdKeys("test1")
	watcher := setupTests(t, readURLsKey, credentialsKey)

	readService, err := newReadService(watcher, readURLsKey, credentialsKey)
	assert.NoError(t, err)
	require.NotNil(t, readService)

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 1)

	assertEnvironment(t, envs[0], "environment", "http://localhost:8080", "user", "pass")
}

func TestWatchingEtcdKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping etcd integration test")
	}

	readURLsKey, credentialsKey := etcdKeys("test2")
	watcher := setupTests(t, readURLsKey, credentialsKey)

	readService, err := newReadService(watcher, readURLsKey, credentialsKey)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
	defer cancel()
	readService.startWatcher(ctx)

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 1)

	assertEnvironment(t, envs[0], "environment", "http://localhost:8080", "user", "pass")

	WatchAddingNewEnvChangingDetails(t, watcher, readService, readURLsKey, credentialsKey)
	WatchRemovingNewEnvChangingDetails(t, watcher, readService, readURLsKey, credentialsKey)
	WatchRemovingOriginalAddingNew(t, watcher, readService, readURLsKey, credentialsKey)
	WatchInvalidReadURLsValidCredentials(t, watcher, readService, readURLsKey, credentialsKey)
	WatchInvalidReadURLValue(t, watcher, readService, readURLsKey, credentialsKey)
	WatchInvalidCredentialsValue(t, watcher, readService, readURLsKey, credentialsKey)
	WatchNewEnvironmentInvalidCredentials(t, watcher, readService, readURLsKey, credentialsKey)
}

func WatchAddingNewEnvChangingDetails(t *testing.T, watcher etcd.Watcher, readService *readService, readURLsKey string, credentialsKey string) {
	go func() { // Validate adding a new environment and changing the original details
		time.Sleep(1 * time.Second)
		api.Set(context.TODO(), credentialsKey, "environment:user-changed:pass-changed,environment2:user-added:pass-added", nil)
		api.Set(context.TODO(), readURLsKey, "environment:http://host-changed:8080,environment2:http://host-added:8080", nil)
	}()

	ctx2, cancel2 := context.WithCancel(context.TODO())
	watcher.Watch(ctx2, readURLsKey, func(val string) {
		assert.Equal(t, "environment:http://host-changed:8080,environment2:http://host-added:8080", val)
		cancel2()
	})

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 2)

	environment := envs[0]
	environment2 := envs[1]

	if envs[0].name != "environment" {
		environment = envs[1]
		environment2 = envs[0]
	}

	assertEnvironment(t, environment, "environment", "http://host-changed:8080", "user-changed", "pass-changed")
	assertEnvironment(t, environment2, "environment2", "http://host-added:8080", "user-added", "pass-added")
}

func WatchRemovingNewEnvChangingDetails(t *testing.T, watcher etcd.Watcher, readService *readService, readURLsKey string, credentialsKey string) {
	go func() { // Validate removing the new environment and changing the original environment again
		time.Sleep(1 * time.Second)
		api.Set(context.TODO(), credentialsKey, "environment:user-changed-back:pass-changed-back", nil)
		api.Set(context.TODO(), readURLsKey, "environment:http://host-changed-back:8080", nil)
	}()

	ctx, cancel := context.WithCancel(context.TODO())
	watcher.Watch(ctx, readURLsKey, func(val string) {
		assert.Equal(t, "environment:http://host-changed-back:8080", val)
		cancel()
	})

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 1)
	assertEnvironment(t, envs[0], "environment", "http://host-changed-back:8080", "user-changed-back", "pass-changed-back")
}

func WatchRemovingOriginalAddingNew(t *testing.T, watcher etcd.Watcher, readService *readService, readURLsKey string, credentialsKey string) {
	go func() { // Validate removing the original environment and adding a new one. (reverse order of keys too)
		time.Sleep(1 * time.Second)
		api.Set(context.TODO(), readURLsKey, ",environment2:http://another-host-added:8080", nil)
		api.Set(context.TODO(), credentialsKey, ",environment2:user-added-another:pass-added-another", nil)
	}()

	ctx3, cancel3 := context.WithCancel(context.TODO())
	watcher.Watch(ctx3, credentialsKey, func(val string) {
		assert.Equal(t, ",environment2:user-added-another:pass-added-another", val)
		cancel3()
	})

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 1)

	environment := envs[0]
	assertEnvironment(t, environment, "environment2", "http://another-host-added:8080", "user-added-another", "pass-added-another")
}

func WatchInvalidReadURLValue(t *testing.T, watcher etcd.Watcher, readService *readService, readURLsKey string, credentialsKey string) {
	go func() { // Invalid values
		time.Sleep(1 * time.Second)
		api.Set(context.TODO(), readURLsKey, "environment2::#", nil)
		api.Set(context.TODO(), credentialsKey, "environment2:user-added-another", nil)
	}()

	ctx4, cancel4 := context.WithCancel(context.TODO())
	watcher.Watch(ctx4, credentialsKey, func(val string) {
		assert.Equal(t, "environment2:user-added-another", val)
		cancel4()
	})

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 0)
}

func WatchInvalidCredentialsValue(t *testing.T, watcher etcd.Watcher, readService *readService, readURLsKey string, credentialsKey string) {
	go func() { // Invalid values
		time.Sleep(1 * time.Second)
		api.Set(context.TODO(), readURLsKey, "environment2:localhost", nil)
		api.Set(context.TODO(), credentialsKey, "environment2:user-added-another", nil)
	}()

	ctx4, cancel4 := context.WithCancel(context.TODO())
	watcher.Watch(ctx4, credentialsKey, func(val string) {
		assert.Equal(t, "environment2:user-added-another", val)
		cancel4()
	})

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 0)
}

func WatchInvalidReadURLsValidCredentials(t *testing.T, watcher etcd.Watcher, readService *readService, readURLsKey string, credentialsKey string) {
	go func() { // Invalid values
		time.Sleep(1 * time.Second)
		api.Set(context.TODO(), readURLsKey, "", nil)
		api.Set(context.TODO(), credentialsKey, "environment2:user-added-another:pass", nil)
	}()

	ctx, cancel := context.WithCancel(context.TODO())
	watcher.Watch(ctx, credentialsKey, func(val string) {
		assert.Equal(t, "environment2:user-added-another:pass", val)
		cancel()
	})

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 1)
}

func WatchNewEnvironmentInvalidCredentials(t *testing.T, watcher etcd.Watcher, readService *readService, readURLsKey string, credentialsKey string) {
	go func() { // Add a new environment with invalid credentials
		time.Sleep(1 * time.Second)
		api.Set(context.TODO(), readURLsKey, "environment3:http://final-host", nil)
		api.Set(context.TODO(), credentialsKey, "environment3", nil)
	}()

	ctx5, cancel5 := context.WithCancel(context.TODO())
	watcher.Watch(ctx5, credentialsKey, func(val string) {
		assert.Equal(t, "environment3", val)
		cancel5()
	})

	envs := readService.GetReadEnvironments()
	require.Len(t, envs, 1)
	assertEnvironment(t, envs[0], "environment3", "http://final-host", "", "")
}

func TestSetupReadClusterFailsReadURLs(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("this shouldn't work", nil)
	watcher.On("Read", "creds-key").Return("", nil)

	readService, err := newReadService(watcher, "read-key", "creds-key")

	assert.Error(t, err)
	assert.Nil(t, readService)
}

func TestSetupReadClusterFailsCredentials(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("env:http://host", nil)
	watcher.On("Read", "creds-key").Return("this shouldn't work", nil)

	readService, err := newReadService(watcher, "read-key", "creds-key")

	assert.Error(t, err)
	assert.Nil(t, readService)
}

func TestSetupReadClusterSucceedsWithEmptyKeys(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("", nil)
	watcher.On("Read", "creds-key").Return("", nil)

	readService, err := newReadService(watcher, "read-key", "creds-key")
	assert.NoError(t, err)

	envs := readService.GetReadEnvironments()
	assert.Len(t, envs, 0)
}

func TestReadKeyFails(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("", errors.New("failed"))

	readService, err := newReadService(watcher, "read-key", "creds-key")
	assert.Error(t, err)
	assert.Nil(t, readService)
}

func TestCredentialsKeyFails(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("", nil)
	watcher.On("Read", "creds-key").Return("", errors.New("failed"))

	readService, err := newReadService(watcher, "read-key", "creds-key")
	assert.Error(t, err)
	assert.Nil(t, readService)
}

func TestReadURLFails(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("environment::#", nil)
	watcher.On("Read", "creds-key").Return("environment:user:pass", nil)

	readService, err := newReadService(watcher, "read-key", "creds-key")
	assert.NoError(t, err)

	envs := readService.GetReadEnvironments()
	assert.Len(t, envs, 0)
}

func TestReadURLsTrailingComma(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("environment:localhost,", nil)
	watcher.On("Read", "creds-key").Return("environment:user:pass", nil)

	readService, err := newReadService(watcher, "read-key", "creds-key")
	assert.NoError(t, err)

	envs := readService.GetReadEnvironments()
	assert.Len(t, envs, 1)
}

func TestCredentialsTrailingComma(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("environment:localhost", nil)
	watcher.On("Read", "creds-key").Return("environment:user:pass,", nil)

	readService, err := newReadService(watcher, "read-key", "creds-key")
	assert.NoError(t, err)

	envs := readService.GetReadEnvironments()
	assert.Len(t, envs, 1)
}

func TestCredentialsFailIncorrectFormat(t *testing.T) {
	watcher := new(etcd.MockWatcher)
	watcher.On("Read", "read-key").Return("environment:http://localhost", nil)
	watcher.On("Read", "creds-key").Return("environment:user", nil)

	readService, err := newReadService(watcher, "read-key", "creds-key")
	assert.Error(t, err)
	assert.Nil(t, readService)
}
