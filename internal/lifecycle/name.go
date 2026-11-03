package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sync/atomic"
)

// ClusterName is a Kind cluster name minted by this process. It is never
// empty: kind's Provider.Delete maps "" to the developer's "kind" cluster.
type ClusterName string

const (
	namePrefix = "kh-"
	// kind warns above 50 characters because node container names derive from it.
	nameMaxLen = 50
)

var (
	validName = regexp.MustCompile(`^[a-z0-9.-]+$`)
	runID     = newRunID()
	nameSeq   atomic.Uint64
)

func newRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("kubeharness: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

func NewClusterName() ClusterName {
	name := fmt.Sprintf("%s%s-%d", namePrefix, runID, nameSeq.Add(1))
	if len(name) > nameMaxLen || !validName.MatchString(name) {
		panic(fmt.Sprintf("kubeharness: generated invalid cluster name %q", name))
	}
	return ClusterName(name)
}
