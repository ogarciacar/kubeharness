//go:build e2e

// Package orders_test runs the M01 scenario twice: once with kubeharness and
// once with plain kind, kubectl, and Go, so the two setups can be compared.
// Both share assertCreateOrder; only the setup functions differ.
package orders_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/ogarciacar/kubeharness"
)

const ordersImage = "kubeharness.local/orders:test"

var buildOnce = sync.OnceValues(func() ([]byte, error) {
	return exec.Command("docker", "build", "-q", "-t", ordersImage, "service").CombinedOutput()
})

func buildOrdersImage(t *testing.T) {
	t.Helper()
	if out, err := buildOnce(); err != nil {
		t.Fatalf("docker build %s: %v\n%s", ordersImage, err, out)
	}
}

func setupWithHarness(t *testing.T, migrations string) (orders, postgres string) {
	return deployOrders(kubeharness.New(t), migrations)
}

func deployOrders(h *kubeharness.Harness, migrations string) (orders, postgres string) {
	h.LoadImage(ordersImage)
	h.Apply("testdata/postgres")
	h.WaitReady("postgres")
	h.RunJob(migrations)
	h.Apply("testdata/orders")
	h.WaitReady("orders")
	return h.Forward("orders", 80), h.Forward("postgres", 5432)
}

// assertCreateOrder posts two different orders and reads each back from
// PostgreSQL by the id the service returned, not from the API, so the test
// trusts neither the service's report of its own write nor its ids.
func assertCreateOrder(t *testing.T, orders, postgres string) {
	t.Helper()
	ctx := context.Background()
	db, err := pgx.Connect(ctx, "postgres://orders:test-only@"+postgres+"/orders")
	if err != nil {
		t.Fatalf("connect to postgres through %s: %v", postgres, err)
	}
	defer db.Close(ctx)
	want := []struct {
		item     string
		quantity int
	}{{"desk lamp", 3}, {"office chair", 5}}
	ids := map[int64]bool{}
	for _, o := range want {
		resp, err := http.Post("http://"+orders+"/orders", "application/json", strings.NewReader(fmt.Sprintf(`{"item":%q,"quantity":%d}`, o.item, o.quantity)))
		if err != nil {
			t.Fatalf("POST /orders: %v", err)
		}
		var created struct{ ID int64 }
		err = json.NewDecoder(resp.Body).Decode(&created)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusCreated || created.ID == 0 || ids[created.ID] {
			t.Fatalf("POST /orders = %d id=%d (%v), want 201 with a new id", resp.StatusCode, created.ID, err)
		}
		ids[created.ID] = true
		var item string
		var quantity int
		if err := db.QueryRow(ctx, "SELECT item, quantity FROM orders WHERE id = $1", created.ID).Scan(&item, &quantity); err != nil {
			t.Fatalf("read order %d back: %v", created.ID, err)
		}
		if item != o.item || quantity != o.quantity {
			t.Fatalf("order %d stored as %q x%d, want %q x%d", created.ID, item, quantity, o.item, o.quantity)
		}
	}
	var rows int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM orders").Scan(&rows); err != nil || rows != len(want) {
		t.Fatalf("orders table holds %d rows (%v), want %d", rows, err, len(want))
	}
}

func TestCreateOrder(t *testing.T) {
	buildOrdersImage(t)
	orders, postgres := setupWithHarness(t, "testdata/migrate")
	assertCreateOrder(t, orders, postgres)
}

// TestHelperBrokenMigration is run in a subprocess by
// TestBrokenMigrationBlocksOrders; it fails on purpose.
func TestHelperBrokenMigration(t *testing.T) {
	if os.Getenv("M01_HELPER") != "broken-migration" {
		t.Skip("helper for TestBrokenMigrationBlocksOrders")
	}
	h := kubeharness.New(t)
	client := kubernetes.NewForConfigOrDie(h.RESTConfig())
	// Registered after New, so it runs before the cluster is deleted.
	t.Cleanup(func() {
		_, err := client.AppsV1().Deployments("default").Get(context.Background(), "orders", metav1.GetOptions{})
		t.Logf("orders deployment exists: %v", err == nil)
	})
	deployOrders(h, "testdata/migrate-broken")
	t.Log("orders deployed")
}

// runHelper runs one helper test in a subprocess and returns its output. The
// helper gets its own -test.timeout, ahead of this test's, so the harness's
// deadline watchdog cleans up there even if this test times out.
func runHelper(t *testing.T, test, mode string) string {
	t.Helper()
	args := []string{"-test.run=^" + test + "$", "-test.v"}
	if deadline, ok := t.Deadline(); ok {
		left := time.Until(deadline) - 30*time.Second
		if left < 2*time.Minute {
			t.Fatalf("-timeout leaves %s for the helper, want at least 2m; a timeout below zero would mean none at all", left.Round(time.Second))
		}
		args = append(args, fmt.Sprintf("-test.timeout=%s", left))
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "M01_HELPER="+mode)
	var out bytes.Buffer
	w := io.MultiWriter(&out, remedyLines{os.Stderr})
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err == nil {
		t.Fatalf("%s passed, want it to fail on the broken migration:\n%s", test, out.String())
	}
	return out.String()
}

func TestBrokenMigrationBlocksOrders(t *testing.T) {
	buildOrdersImage(t)
	got := runHelper(t, "TestHelperBrokenMigration", "broken-migration")
	for _, want := range []string{
		"kubeharness: run job: job migrate failed",
		"reason=BackoffLimitExceeded",
		"terminated exit=3",
		`relation "customers" does not exist`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("failure output lacks %q", want)
		}
	}
	switch {
	case strings.Contains(got, "orders deployment exists: true"):
		t.Error("the orders Deployment was applied although the migration failed")
	case !strings.Contains(got, "orders deployment exists: false"):
		t.Error("the helper never checked whether the orders Deployment exists")
	}
	if strings.Contains(got, "orders deployed") {
		t.Error("the test went past the failed migration")
	}
	if !regexp.MustCompile(`cause=test-failed cleanup=succeeded remaining=0`).MatchString(got) {
		t.Error("no report line with a failed test and a succeeded cleanup")
	}
	if t.Failed() {
		t.Logf("helper output:\n%s", got)
	}
}

// TestHelperBaselineBrokenMigration is run in a subprocess by
// TestBaselineStopsAtABrokenMigration; it fails on purpose.
func TestHelperBaselineBrokenMigration(t *testing.T) {
	if os.Getenv("M01_HELPER") != "baseline-broken-migration" {
		t.Skip("helper for TestBaselineStopsAtABrokenMigration")
	}
	setupBaseline(t, "testdata/migrate-broken")
	t.Log("orders deployed")
}

// TestBaselineStopsAtABrokenMigration keeps the comparison fair: the baseline
// must stop at a failed migration and show its logs, as the harness does.
func TestBaselineStopsAtABrokenMigration(t *testing.T) {
	buildOrdersImage(t)
	got := runHelper(t, "TestHelperBaselineBrokenMigration", "baseline-broken-migration")
	if !strings.Contains(got, "migration failed") || !strings.Contains(got, `relation "customers" does not exist`) || strings.Contains(got, "orders deployed") {
		t.Fatalf("baseline did not stop at the broken migration with its logs:\n%s", got)
	}
}

// TestOrdersStayUnreadyUntilTheMigration checks that readiness means the
// service can store orders: before the migration creates the table, the
// Deployment must not become available.
func TestOrdersStayUnreadyUntilTheMigration(t *testing.T) {
	buildOrdersImage(t)
	h := kubeharness.New(t)
	client := kubernetes.NewForConfigOrDie(h.RESTConfig())
	h.LoadImage(ordersImage)
	h.Apply("testdata/postgres")
	h.WaitReady("postgres")
	h.Apply("testdata/orders")
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(time.Second) {
		d, err := client.AppsV1().Deployments("default").Get(t.Context(), "orders", metav1.GetOptions{})
		if err == nil && d.Status.AvailableReplicas > 0 {
			t.Fatal("orders became available before the migration created its table")
		}
	}
	h.RunJob("testdata/migrate")
	h.WaitReady("orders")
}

// remedyLines passes on only the helper's "creating cluster" line, which names
// the cluster and how to remove it, so a parent timeout cannot hide it.
type remedyLines struct{ w io.Writer }

func (r remedyLines) Write(p []byte) (int, error) {
	for _, line := range bytes.SplitAfter(p, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("kubeharness: creating cluster")) {
			_, _ = r.w.Write(line)
		}
	}
	return len(p), nil
}
