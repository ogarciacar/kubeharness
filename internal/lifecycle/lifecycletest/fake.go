// Package lifecycletest provides a scripted fake cluster provider for tests.
package lifecycletest

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Fake is an in-memory cluster provider whose failures tests script through
// its fields.
type Fake struct {
	Mu        sync.Mutex
	Inventory map[string]bool
	Creates   int
	Deletes   int

	ListErr     error
	ListBlock   chan struct{}
	DeleteDelay time.Duration
	// CreateAllocatesThenFails models kind's kubeconfig-export failure: nodes
	// exist but Create returns an error.
	CreateAllocatesThenFails bool
	CreateErr                error
	CreateBlock              chan struct{}
	DeleteErrs               []error
	DeleteKeepsCluster       bool
	DeleteBlock              chan struct{}
}

func NewFake() *Fake { return &Fake{Inventory: map[string]bool{}} }

const FakeKubeconfig = `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://127.0.0.1:1"}}]
users: [{name: u, user: {}}]
contexts: [{name: x, context: {cluster: c, user: u}}]
current-context: x
`

func (f *Fake) Create(name, kubeconfigPath string) error {
	if filepath.IsAbs(kubeconfigPath) {
		if err := os.WriteFile(kubeconfigPath, []byte(FakeKubeconfig), 0o600); err != nil {
			return err
		}
	}
	f.Mu.Lock()
	f.Creates++
	block := f.CreateBlock
	f.Mu.Unlock()
	if block != nil {
		<-block
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.CreateErr != nil {
		return f.CreateErr
	}
	f.Inventory[name] = true
	if f.CreateAllocatesThenFails {
		return errors.New("failed to export kubeconfig")
	}
	return nil
}

func (f *Fake) Delete(name, _ string) error {
	f.Mu.Lock()
	f.Deletes++
	block := f.DeleteBlock
	var err error
	if len(f.DeleteErrs) > 0 {
		err, f.DeleteErrs = f.DeleteErrs[0], f.DeleteErrs[1:]
	}
	f.Mu.Unlock()
	if block != nil {
		<-block
	}
	time.Sleep(f.DeleteDelay)
	if err != nil {
		return err
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if !f.DeleteKeepsCluster {
		delete(f.Inventory, name)
	}
	return nil
}

func (f *Fake) List() ([]string, error) {
	f.Mu.Lock()
	block := f.ListBlock
	f.Mu.Unlock()
	if block != nil {
		<-block
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	var names []string
	for n := range f.Inventory {
		names = append(names, n)
	}
	return names, nil
}

func (f *Fake) Counts() (creates, deletes, live int) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return f.Creates, f.Deletes, len(f.Inventory)
}
