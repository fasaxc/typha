// Copyright (c) 2017 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fv_tests_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"fmt"
	"sync"

	log "github.com/Sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/typha/pkg/snapcache"
	"github.com/projectcalico/typha/pkg/syncclient"
	"github.com/projectcalico/typha/pkg/syncserver"
)

// Tests that rely on starting a real Server (on a real TCP port) in this process.
// We driver the server via a real snapshot cache usnig the snapshot cache's function
// API.
var _ = Describe("With an in-process Server", func() {
	var cacheCxt context.Context
	var cacheCancel context.CancelFunc
	var cache *snapcache.Cache
	var server *syncserver.Server
	var serverCxt context.Context
	var serverCancel context.CancelFunc

	BeforeEach(func() {
		cache = snapcache.New(snapcache.Config{
			// Set the batch size small so we can force new Breadcrumbs easily.
			MaxBatchSize: 10,
			// Reduce the wake up interval from the default to give us faster tear down.
			WakeUpInterval: 50 * time.Millisecond,
		})
		server = syncserver.New(cache, syncserver.Config{
			PingInterval: 20 * time.Second,
			Port:         syncserver.PortRandom,
		})
		cacheCxt, cacheCancel = context.WithCancel(context.Background())
		cache.Start(cacheCxt)
		serverCxt, serverCancel = context.WithCancel(context.Background())
		server.Start(serverCxt)
	})

	It("should choose a port", func() {
		Expect(server.Port()).ToNot(BeZero())
	})

	Describe("with a client connection", func() {
		var clientCxt context.Context
		var clientCancel context.CancelFunc
		var client *syncclient.SyncerClient
		var recorder *stateRecorder

		BeforeEach(func() {
			clientCxt, clientCancel = context.WithCancel(context.Background())
			recorder = &stateRecorder{
				kvs: map[string]api.Update{},
			}
			client = syncclient.New(
				fmt.Sprintf("127.0.0.1:%d", server.Port()),
				"test-version",
				"test-host",
				"test-info",
				recorder,
			)
			err := client.StartContext(clientCxt)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			clientCancel()
			if client != nil {
				client.Finished.Wait()
			}
		})

		It("should pass through a KV and status", func() {
			update := api.Update{
				KVPair: model.KVPair{
					Key:      model.GlobalConfigKey{Name: "foobar"},
					Value:    "bazzbiff",
					Revision: "1234",
					TTL:      12,
				},
				UpdateType: api.UpdateTypeKVNew,
			}
			cache.OnStatusUpdated(api.ResyncInProgress)
			cache.OnUpdates([]api.Update{update})
			cache.OnStatusUpdated(api.InSync)
			Eventually(recorder.KVs).Should(Equal(map[string]api.Update{
				"/calico/v1/config/foobar": update,
			}))
			Eventually(recorder.status).Should(Equal(api.InSync))
		})

		It("should pass through many KVs", func() {
			expectedEndState := map[string]api.Update{}
			cache.OnStatusUpdated(api.ResyncInProgress)
			for i := 0; i < 100; i++ {
				update := api.Update{
					KVPair: model.KVPair{
						Key: model.GlobalConfigKey{
							Name: fmt.Sprintf("foo%v", i),
						},
						Value:    fmt.Sprintf("baz%v", i),
						Revision: fmt.Sprintf("%v", i),
					},
					UpdateType: api.UpdateTypeKVNew,
				}
				path, err := model.KeyToDefaultPath(update.Key)
				Expect(err).NotTo(HaveOccurred())
				expectedEndState[path] = update
				cache.OnUpdates([]api.Update{update})
			}
			cache.OnStatusUpdated(api.InSync)
			Eventually(recorder.Status, time.Second).Should(Equal(api.InSync))
			Eventually(recorder.KVs).Should(Equal(expectedEndState))
		})
	})

	AfterEach(func() {
		if server != nil {
			serverCancel()
			log.Info("Waiting for server to shut down")
			server.Finished.Wait()
			log.Info("Done waiting for server to shut down")
		}
		if cache != nil {
			cacheCancel()
		}
	})
})

type stateRecorder struct {
	L      sync.Mutex
	status api.SyncStatus
	kvs    map[string]api.Update
	err    error
}

func (r *stateRecorder) KVs() map[string]api.Update {
	r.L.Lock()
	defer r.L.Unlock()

	kvsCpy := map[string]api.Update{}
	for k, v := range r.kvs {
		kvsCpy[k] = v
	}
	return kvsCpy
}

func (r *stateRecorder) Status() api.SyncStatus {
	r.L.Lock()
	defer r.L.Unlock()

	return r.status
}

func (r *stateRecorder) OnUpdates(updates []api.Update) {
	r.L.Lock()
	defer r.L.Unlock()

	for _, u := range updates {
		path, err := model.KeyToDefaultPath(u.Key)
		if err != nil {
			r.err = err
			continue
		}
		if u.Value == nil {
			delete(r.kvs, path)
		} else {
			r.kvs[path] = u
		}
	}
}

func (r *stateRecorder) OnStatusUpdated(status api.SyncStatus) {
	r.L.Lock()
	defer r.L.Unlock()

	r.status = status
}