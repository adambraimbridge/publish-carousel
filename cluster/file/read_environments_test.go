package file

import (
	"testing"
	"fmt"
	"net/url"

	"github.com/stretchr/testify/assert"
	"github.com/Financial-Times/publish-carousel/cluster"
	"errors"
	"io/ioutil"
	"os"
	"github.com/Sirupsen/logrus"
	"time"
	"github.com/Financial-Times/publish-carousel/file"
	"path/filepath"
	"context"
	"reflect"
)

func TestParseEnvironmentsSuccessfully(t *testing.T) {
	testCases := []struct {
		description      string
		readURLs         string
		credentials      string
		expectedEnvsSize int
		expectedEnvSvc   environmentService
	}{
		{
			description:      "Valid readURLs string and empty credentials string",
			readURLs:         "environment1:http://address1,environment2:http://address2",
			credentials:      "",
			expectedEnvsSize: 2,
			expectedEnvSvc: environmentService{
				environments: map[string]readEnvironment{
					"environment1": {
						name: "environment1",
						readURL: &url.URL{
							Host:   "address1",
							Scheme: "http",
						},
					},
					"environment2": {
						name: "environment2",
						readURL: &url.URL{
							Host:   "address2",
							Scheme: "http",
						},
					},
				},
			},
		},
		{
			description:      "Valid readURLs and credentials string",
			readURLs:         "environment1:https://address1,environment2:http://address2",
			credentials:      "environment1:user:password",
			expectedEnvsSize: 2,
			expectedEnvSvc: environmentService{
				environments: map[string]readEnvironment{
					"environment1": {
						name: "environment1",
						readURL: &url.URL{
							Host:   "address1",
							Scheme: "https",
						},
						credentials: &credentials{
							username: "user",
							password: "password",
						},
					},
					"environment2": {
						name: "environment2",
						readURL: &url.URL{
							Host:   "address2",
							Scheme: "http",
						},
					},
				},
			},
		},
		{
			description:      "readURLs and credentials strings contain only spaces",
			readURLs:         " ",
			credentials:      " ",
			expectedEnvsSize: 0,
			expectedEnvSvc: environmentService{
				environments: map[string]readEnvironment{},
			},
		},
		{
			description:      "readURLs and credentials strings are empty",
			readURLs:         "",
			credentials:      "",
			expectedEnvsSize: 0,
			expectedEnvSvc: environmentService{
				environments: map[string]readEnvironment{},
			},
		},
		{
			description:      "readURLs and credentials strings contain only commas",
			readURLs:         ",,",
			credentials:      ",,",
			expectedEnvsSize: 0,
			expectedEnvSvc: environmentService{
				environments: map[string]readEnvironment{},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s", tc.description), func(t *testing.T) {
			envs, err := parseEnvironments(tc.readURLs, tc.credentials)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedEnvsSize, len(envs))
			for expectedEnvName, expectedEnv := range tc.expectedEnvSvc.environments {
				assert.Contains(t, envs, expectedEnvName)
				assert.Equal(t, expectedEnv.name, envs[expectedEnvName].name)
				assert.Equal(t, expectedEnv.credentials, envs[expectedEnvName].credentials)
				assert.Equal(t, expectedEnv.readURL.Host, envs[expectedEnvName].readURL.Host)
				assert.Equal(t, expectedEnv.readURL.Scheme, envs[expectedEnvName].readURL.Scheme)
			}
		})
	}
}

func TestParseEnvironmentsWithFailure(t *testing.T) {
	testCases := []struct {
		readURLs         string
		credentials      string
		description      string
		expectedEnvsSize int
		expectedEnvs     environmentService
	}{
		{
			readURLs:         "environment",
			credentials:      "",
			description:      "readURLs string is missing the environment url",
			expectedEnvsSize: 0,
			expectedEnvs: environmentService{
				environments: map[string]readEnvironment{},
			},
		},
		{
			readURLs:         "",
			credentials:      "environment:user",
			description:      "credentials string is missing the password",
			expectedEnvsSize: 0,
			expectedEnvs: environmentService{
				environments: map[string]readEnvironment{},
			},
		},
		{
			readURLs:         "",
			credentials:      "environment",
			description:      "credentials string is missing user and password",
			expectedEnvsSize: 0,
			expectedEnvs: environmentService{
				environments: map[string]readEnvironment{},
			},
		},
		{
			readURLs:         "environment1:http://[::1]:namedport,environment2:http://localhost",
			credentials:      "",
			description:      "readURLs string contains an invalid url",
			expectedEnvsSize: 1,
			expectedEnvs: environmentService{
				environments: map[string]readEnvironment{
					"environment2": {
						name: "environment2",
						readURL: &url.URL{
							Host:   "localhost",
							Scheme: "http",
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s", tc.description), func(t *testing.T) {
			envs, err := parseEnvironments(tc.readURLs, tc.credentials)
			assert.Error(t, err)
			assert.Equal(t, tc.expectedEnvsSize, len(envs))
			if tc.expectedEnvsSize != 0 {
				for expectedEnvName, expectedEnv := range tc.expectedEnvs.environments {
					assert.Contains(t, envs, expectedEnvName)
					env := envs[expectedEnvName]
					assertEnvironment(t, &expectedEnv, &env)
				}
			}
		})
	}
}

func TestGetEnvironments(t *testing.T) {
	expectedReadEnvs := []readEnvironment{
		{
			name: "environment1",
			readURL: &url.URL{
				Host:   "address1",
				Scheme: "http",
			},
			credentials: &credentials{
				username: "user1",
				password: "password1",
			}},
		{
			name: "environment2",
			readURL: &url.URL{
				Host:   "address2",
				Scheme: "http",
			},
			credentials: &credentials{
				username: "user2",
				password: "password2",
			}},
	}

	watcher := new(cluster.MockWatcher)
	watcher.On("Read", "envsFile").Return("environment1:http://address1,environment2:http://address2", nil)
	watcher.On("Read", "credsFile").Return("environment1:user1:password1,environment2:user2:password2", nil)

	envSrv, _ := newEnvironmentService(watcher, "envsFile", "credsFile")
	envs := envSrv.GetEnvironments()

	assert.Equal(t, 2, len(envs))
	assert.Contains(t, envs, expectedReadEnvs[0])
	assert.Contains(t, envs, expectedReadEnvs[1])
}

func TestNewEnvironmentService(t *testing.T) {
	testCases := []struct {
		description    string
		expectError    bool
		readURLs       string
		credentials    string
		expectedEnvSvc environmentService
	}{
		{
			description: "Valid environment and credential strings",
			expectError: false,
			readURLs:    "environment1:https://address1,environment2:http://address2",
			credentials: "environment1:user1:password1,environment2:user2:password2",
			expectedEnvSvc: environmentService{
				environments: map[string]readEnvironment{
					"environment1": {
						name: "environment1",
						readURL: &url.URL{
							Host:   "address1",
							Scheme: "https",
						},
						credentials: &credentials{
							username: "user1",
							password: "password1",
						},
					},
					"environment2": {
						name: "environment2",
						readURL: &url.URL{
							Host:   "address2",
							Scheme: "http",
						},
						credentials: &credentials{
							username: "user2",
							password: "password2",
						},
					},
				},
			},
		},
		{
			description: "First environment is missing colons",
			expectError: true,
			readURLs:    "environment1http//address1,environment2:http://address2",
			credentials: "",
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s", tc.description), func(t *testing.T) {

			watcher := new(cluster.MockWatcher)

			watcher.On("Read", "envsFile").Return(tc.readURLs, nil)
			watcher.On("Read", "credsFile").Return(tc.credentials, nil)

			envSrv, err := newEnvironmentService(watcher, "envsFile", "credsFile")

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedEnvSvc.environments), len(envSrv.environments))
				for key, value := range tc.expectedEnvSvc.environments {
					assert.Contains(t, envSrv.environments, key)
					env := envSrv.environments[key]
					assertEnvironment(t, &value, &env)
				}
			}
		})
	}
}

func TestNewEnvironmentServiceFileReadErrors(t *testing.T) {
	testCases := []struct {
		description                 string
		expectEnvironmentsFileError bool
		expectCredentialsFileError  bool
	}{
		{
			description:                 "Error reading environments",
			expectEnvironmentsFileError: true,
			expectCredentialsFileError:  false,
		},
		{
			description:                 "Error reading credentials",
			expectEnvironmentsFileError: false,
			expectCredentialsFileError:  true,
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s", tc.description), func(t *testing.T) {
			watcher := new(cluster.MockWatcher)

			if tc.expectCredentialsFileError {
				watcher.On("Read", "credsFile").Return("", errors.New("Cannot read file!"))
			} else {
				watcher.On("Read", "credsFile").Return("mock credentials", nil)
			}

			if tc.expectEnvironmentsFileError {
				watcher.On("Read", "envsFile").Return("", errors.New("Cannot read file!"))
			} else {
				watcher.On("Read", "envsFile").Return("mock envs", nil)
			}

			_, err := newEnvironmentService(watcher, "envsFile", "credsFile")

			assert.Error(t, err)
		})
	}
}

func TestEnvironmentServiceWatcher(t *testing.T) {
	testCases := []struct {
		description        string
		environmentUpdate  bool
		credentialsUpdate  bool
		initialReadURLs    string
		updatedReadURLs    string
		initialCredentials string
		updatedCredentials string
		initialEnvs        map[string]readEnvironment
		updatedEnvs        map[string]readEnvironment
	}{
		{
			description:        "Environments file ONLY is updated",
			environmentUpdate:  true,
			credentialsUpdate:  false,
			initialReadURLs:    "environment1:https://address1,environment2:http://address2",
			updatedReadURLs:    "environment1:https://address3,environment2:http://address2",
			initialCredentials: "environment1:user1:password1,environment2:user2:password2",
			updatedCredentials: "environment1:user1:password1,environment2:user2:password2",
			initialEnvs: map[string]readEnvironment{
				"environment1": {
					name: "environment1",
					readURL: &url.URL{
						Host:   "address1",
						Scheme: "https",
					},
					credentials: &credentials{
						username: "user1",
						password: "password1",
					},
				},
				"environment2": {
					name: "environment2",
					readURL: &url.URL{
						Host:   "address2",
						Scheme: "http",
					},
					credentials: &credentials{
						username: "user2",
						password: "password2",
					},
				},
			},
			updatedEnvs: map[string]readEnvironment{
				"environment1": {
					name: "environment1",
					readURL: &url.URL{
						Host:   "address3",
						Scheme: "https",
					},
					credentials: &credentials{
						username: "user1",
						password: "password1",
					},
				},
				"environment2": {
					name: "environment2",
					readURL: &url.URL{
						Host:   "address2",
						Scheme: "http",
					},
					credentials: &credentials{
						username: "user2",
						password: "password2",
					},
				},
			},
		}, {
			description:        "Credentials file ONLY is updated",
			environmentUpdate:  false,
			credentialsUpdate:  true,
			initialReadURLs:    "environment1:https://address1,environment2:http://address2",
			updatedReadURLs:    "environment1:https://address1,environment2:http://address2",
			initialCredentials: "environment1:user1:password1,environment2:user2:password2",
			updatedCredentials: "environment1:user5:password5,environment2:user6:password6",
			initialEnvs: map[string]readEnvironment{
				"environment1": {
					name: "environment1",
					readURL: &url.URL{
						Host:   "address1",
						Scheme: "https",
					},
					credentials: &credentials{
						username: "user1",
						password: "password1",
					},
				},
				"environment2": {
					name: "environment2",
					readURL: &url.URL{
						Host:   "address2",
						Scheme: "http",
					},
					credentials: &credentials{
						username: "user2",
						password: "password2",
					},
				},
			},
			updatedEnvs: map[string]readEnvironment{
				"environment1": {
					name: "environment1",
					readURL: &url.URL{
						Host:   "address1",
						Scheme: "https",
					},
					credentials: &credentials{
						username: "user5",
						password: "password5",
					},
				},
				"environment2": {
					name: "environment2",
					readURL: &url.URL{
						Host:   "address2",
						Scheme: "http",
					},
					credentials: &credentials{
						username: "user6",
						password: "password6",
					},
				},
			},
		}, {
			description:        "Credentials file AND Environments file is updated",
			environmentUpdate:  true,
			credentialsUpdate:  true,
			initialReadURLs:    "environment1:https://address1,environment2:http://address2",
			updatedReadURLs:    "environment4:https://address4,environment5:http://address5",
			initialCredentials: "environment1:user1:password1,environment2:user2:password2",
			updatedCredentials: "environment4:user4:password4,environment5:user5:password5",
			initialEnvs: map[string]readEnvironment{
				"environment1": {
					name: "environment1",
					readURL: &url.URL{
						Host:   "address1",
						Scheme: "https",
					},
					credentials: &credentials{
						username: "user1",
						password: "password1",
					},
				},
				"environment2": {
					name: "environment2",
					readURL: &url.URL{
						Host:   "address2",
						Scheme: "http",
					},
					credentials: &credentials{
						username: "user2",
						password: "password2",
					},
				},
			},
			updatedEnvs: map[string]readEnvironment{
				"environment4": {
					name: "environment4",
					readURL: &url.URL{
						Host:   "address4",
						Scheme: "https",
					},
					credentials: &credentials{
						username: "user4",
						password: "password4",
					},
				},
				"environment5": {
					name: "environment5",
					readURL: &url.URL{
						Host:   "address5",
						Scheme: "http",
					},
					credentials: &credentials{
						username: "user5",
						password: "password5",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s", tc.description), func(t *testing.T) {
			tempDir, _ := ioutil.TempDir(os.TempDir(), "testDir")
			envsFile, _ := ioutil.TempFile(tempDir, "read-environments")
			envsFile.WriteString(tc.initialReadURLs)
			envsFile.Close()
			credsFile, _ := ioutil.TempFile(tempDir, "credentials")
			credsFile.WriteString(tc.initialCredentials)
			credsFile.Close()

			ctx, cancel := context.WithCancel(context.Background())

			watcher, _ := file.NewFileWatcher([]string{tempDir}, time.Second*1)
			envSrv, _ := newEnvironmentService(watcher, filepath.Base(envsFile.Name()), filepath.Base(credsFile.Name()))
			envSrv.startWatcher(ctx, filepath.Base(envsFile.Name()), filepath.Base(credsFile.Name()))
			initialEnvironments := envSrv.environments
			assert.Equal(t, len(tc.initialEnvs), len(initialEnvironments))
			assert.True(t, reflect.DeepEqual(tc.initialEnvs, initialEnvironments))

			if tc.environmentUpdate {
				ioutil.WriteFile(envsFile.Name(), []byte(tc.updatedReadURLs), 0600)
			}
			if tc.credentialsUpdate {
				ioutil.WriteFile(credsFile.Name(), []byte(tc.updatedCredentials), 0600)
			}

			time.Sleep(2 * time.Second)
			cancel()
			updatedEnvironments := envSrv.environments
			assert.Equal(t, len(tc.updatedEnvs), len(updatedEnvironments))

			//fmt.Printf("EXPECTED\n%# v", pretty.Formatter(tc.updatedEnvs))
			//fmt.Printf("ACTUAL\n%# v", pretty.Formatter(updatedEnvironments))
			assert.True(t, reflect.DeepEqual(tc.updatedEnvs, updatedEnvironments))

			cleanupDir(tempDir)
		})
	}
}

func assertEnvironment(t *testing.T, expectedEnv *readEnvironment, env *readEnvironment) {
	assert.Equal(t, expectedEnv.name, env.name)
	assert.Equal(t, expectedEnv.credentials, env.credentials)
	assert.Equal(t, expectedEnv.readURL.Host, env.readURL.Host)
	assert.Equal(t, expectedEnv.readURL.Scheme, env.readURL.Scheme)
}

func cleanupDir(tempDir string) {
	err := os.RemoveAll(tempDir)
	if err != nil {
		logrus.WithError(err).Error("Cannot remove temp dir", tempDir)
	}
}
