// No build tag: this file runs in the plain `go test ./...` CI step, so the
// fixture guard and the ledger judge are checked without a cluster. The e2e
// test in g03_test.go uses the same functions.
package gateway_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// g03FlipOrder names, in order, the testdata/g03-route directories
// TestG03WeightFlip applies. The first creates the route; each later one
// replaces it. Names are backend names: each directory's route puts weight
// 100 on the backend it is named for.
var g03FlipOrder = []string{"blue", "green", "blue"}

const g03RouteRoot = "testdata/g03-route"

const (
	// g03Hold is how long traffic runs after a flip is ready before the next
	// Apply, and after the last flip before the ledger is judged.
	g03Hold = 3 * time.Second
	// g03MinHold is the fewest ledger requests a hold may contain. Fewer
	// means the traffic goroutine stalled and an all-correct hold proves
	// nothing.
	g03MinHold = 20
)

// flip is one Apply of a route directory and the backend the route in it
// selects. backend is read from the fixture, never written separately.
type flip struct {
	dir     string
	backend string
}

// g03Flips is loadFlips over the committed fixtures, failing t on error.
func g03Flips(t testing.TB) []flip {
	t.Helper()
	flips, err := loadFlips(g03RouteRoot, g03FlipOrder)
	if err != nil {
		t.Fatalf("g03 fixtures: %v", err)
	}
	return flips
}

// loadFlips reads root/<name>/ for each name in order and returns the flips.
// It fails unless:
//
//   - each directory holds exactly one object, an HTTPRoute, and every
//     directory names the same route (namespace/name);
//   - the routes are equal once every spec.rules[].backendRefs[].weight is
//     removed, so an SSA Force Apply of one never drops a field the other set;
//   - each route puts weight 100 on exactly one backendRef, the one called
//     name, and weight 0 on every other;
//   - no two consecutive names are equal, so every Apply after the first
//     changes the spec and bumps metadata.generation (the same-generation
//     no-op AwaitRoute can't distinguish from success).
//
// Non-consecutive repeats (blue, green, blue) are allowed. Generation 3 has
// the same spec as generation 1 but differs from the live generation 2.
func loadFlips(root string, order []string) ([]flip, error) {
	if len(order) == 0 {
		return nil, fmt.Errorf("flip order is empty")
	}
	flips := make([]flip, 0, len(order))
	routes := make([]*unstructured.Unstructured, 0, len(order))
	dirs := make([]string, 0, len(order))
	for i, name := range order {
		if i > 0 && name == order[i-1] {
			return nil, fmt.Errorf("flip %d repeats %q; the Apply would not change the route", i, name)
		}
		dir := filepath.Join(root, name)
		objs, err := readManifests(dir)
		if err != nil {
			return nil, err
		}
		if len(objs) != 1 || objs[0].GetKind() != "HTTPRoute" {
			return nil, fmt.Errorf("%s: exactly one object, an HTTPRoute, got %d", dir, len(objs))
		}
		backend, err := selected(objs[0])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		if backend != name {
			return nil, fmt.Errorf("%s selects %q, dir is %s", dir, backend, name)
		}
		routes = append(routes, objs[0])
		dirs = append(dirs, dir)
		flips = append(flips, flip{dir: dir, backend: name})
	}
	first := routes[0]
	bareFirst := withoutWeights(first)
	id := first.GetNamespace() + "/" + first.GetName()
	for i := 1; i < len(routes); i++ {
		r := routes[i]
		got := r.GetNamespace() + "/" + r.GetName()
		if got != id {
			return nil, fmt.Errorf("%s and %s name different routes %s and %s", dirs[0], dirs[i], id, got)
		}
		bare := withoutWeights(r)
		if !equality.Semantic.DeepEqual(bareFirst, bare) {
			return nil, fmt.Errorf("%s and %s differ at %s", dirs[0], dirs[i], diffPath(bareFirst.Object, bare.Object))
		}
	}
	return flips, nil
}

// selected returns the name of the one backendRef with weight 100 across all
// rules of route, failing if any ref lacks a weight, has a weight other than
// 0 or 100, or if not exactly one ref has 100. Weight must be present as an
// integer (yamlInt); a missing weight is not treated as zero.
func selected(route *unstructured.Unstructured) (string, error) {
	rules, found, err := unstructured.NestedSlice(route.Object, "spec", "rules")
	if err != nil || !found {
		return "", fmt.Errorf("HTTPRoute %s/%s has no spec.rules", route.GetNamespace(), route.GetName())
	}
	var picked string
	var n100 int
	for ri, rule := range rules {
		m, ok := rule.(map[string]any)
		if !ok {
			return "", fmt.Errorf("spec.rules[%d] is not an object", ri)
		}
		refs, _ := m["backendRefs"].([]any)
		for bi, ref := range refs {
			rm, ok := ref.(map[string]any)
			if !ok {
				return "", fmt.Errorf("spec.rules[%d].backendRefs[%d] is not an object", ri, bi)
			}
			name, _ := rm["name"].(string)
			weight, ok := yamlInt(rm["weight"])
			if !ok {
				return "", fmt.Errorf("backendRef %q has no weight", name)
			}
			if weight != 0 && weight != 100 {
				return "", fmt.Errorf("backendRef %q has weight %d, want 0 or 100", name, weight)
			}
			if weight == 100 {
				n100++
				picked = name
			}
		}
	}
	if n100 != 1 {
		return "", fmt.Errorf("want exactly one backendRef with weight 100, found %d", n100)
	}
	return picked, nil
}

// yamlInt accepts the number types the YAML decoder leaves in an
// unstructured map (int, int64, float64). Missing or wrong type → false.
func yamlInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

// withoutWeights returns a deep copy of route with every backendRef weight
// removed. loadFlips compares these copies.
func withoutWeights(route *unstructured.Unstructured) *unstructured.Unstructured {
	out := route.DeepCopy()
	// NestedSlice returns a copy. Delete on the live maps of this deep copy.
	raw, found, err := unstructured.NestedFieldNoCopy(out.Object, "spec", "rules")
	if err != nil || !found {
		return out
	}
	rules, ok := raw.([]any)
	if !ok {
		return out
	}
	for _, rule := range rules {
		m, ok := rule.(map[string]any)
		if !ok {
			continue
		}
		refs, ok := m["backendRefs"].([]any)
		if !ok {
			continue
		}
		for _, ref := range refs {
			rm, ok := ref.(map[string]any)
			if !ok {
				continue
			}
			delete(rm, "weight")
		}
	}
	return out
}

// readManifests decodes every .yaml/.yml file in dir in file-name order, as
// Harness.Apply reads them. paymentsReplicas moves onto this helper so the
// example has one YAML decode loop.
func readManifests(dir string) ([]*unstructured.Unstructured, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if ext := filepath.Ext(e.Name()); !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(files)
	var objs []*unstructured.Unstructured
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		fileObjs, err := decodeManifests(f, data)
		if err != nil {
			return nil, err
		}
		objs = append(objs, fileObjs...)
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("no manifests in %s", dir)
	}
	return objs, nil
}

// decodeManifests parses YAML or JSON documents the way internal/kube.ReadManifests
// does: empty documents are skipped, and a kind: List contributes its items.
func decodeManifests(source string, data []byte) ([]*unstructured.Unstructured, error) {
	var objs []*unstructured.Unstructured
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for i := 1; ; i++ {
		var m map[string]any
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			return objs, nil
		} else if err != nil {
			return nil, fmt.Errorf("%s document %d: %w", source, i, err)
		}
		if len(m) == 0 {
			continue
		}
		u := &unstructured.Unstructured{Object: m}
		items := []*unstructured.Unstructured{u}
		if u.IsList() {
			list, err := u.ToList()
			if err != nil {
				return nil, fmt.Errorf("%s document %d: %w", source, i, err)
			}
			items = items[:0]
			for j := range list.Items {
				items = append(items, &list.Items[j])
			}
		}
		for _, item := range items {
			if item.GetKind() == "" || item.GetAPIVersion() == "" || item.GetName() == "" {
				return nil, fmt.Errorf("%s document %d: kind, apiVersion, and metadata.name are required", source, i)
			}
			objs = append(objs, item)
		}
	}
}

// readBackend accepts a sample only when the status is 200 and the
// X-Backend header equals the JSON backend field. Disagreement is an
// error observation so it cannot extend a hold window. The body read stops
// at 1 MiB. The caller closes resp.Body.
func readBackend(resp *http.Response) (string, error) {
	header := resp.Header.Get("X-Backend")
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("status %d X-Backend %q: read body: %w", resp.StatusCode, header, err)
	}
	var got struct {
		Backend string `json:"backend"`
	}
	decErr := json.Unmarshal(body, &got)
	if resp.StatusCode != http.StatusOK || decErr != nil || header == "" || header != got.Backend {
		snippet := body
		if len(snippet) > 80 {
			snippet = snippet[:80]
		}
		return "", fmt.Errorf("status %d X-Backend %q body %q backend %q", resp.StatusCode, header, snippet, got.Backend)
	}
	return header, nil
}

// TestG03DirHasNoRoute fails if testdata/g03 contains an HTTPRoute or
// ReferenceGrant. The route lives only under testdata/g03-route/<flip>/.
func TestG03DirHasNoRoute(t *testing.T) {
	objs, err := readManifests("testdata/g03")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range objs {
		switch u.GetKind() {
		case "HTTPRoute", "ReferenceGrant":
			t.Fatalf("testdata/g03 contains %s %s/%s; the route lives under testdata/g03-route", u.GetKind(), u.GetNamespace(), u.GetName())
		}
	}
}

// sample is one request the traffic goroutine sent through the Gateway.
// The goroutine is the only writer; it hands the slice over once, on stop.
type sample struct {
	sent    time.Time // before client.Do; requests are classified by send time
	status  int       // 0 when Do returned an error
	backend string    // X-Backend; "" when absent or on error
	err     string    // transport error text, "" on a response
}

// mark is one flip as the test goroutine timed it. All three times come
// from time.Now on the test goroutine, so they share a monotonic clock with
// sample.sent.
type mark struct {
	to      string    // backend the flip selects (flip.backend)
	applied time.Time // just before h.Apply
	status  time.Time // AwaitRoute(RouteReady) returned: new generation acknowledged
	ready   time.Time // AwaitResponse(servedBy(to)) returned: both clocks have seen the flip
}

// transition is what the ledger says about one flip after the first.
// String renders the single "g03 flip" log line convergence.sh parses.
type transition struct {
	from, to  string
	status    time.Duration // mark.status - mark.applied
	dataplane time.Duration // sent of first request after which every request hit `to`, minus mark.applied
	old, new  int           // requests in [applied, ready) served by from / to
	errs      int           // requests in [applied, ready) that were not a 200 from from or to
	switches  int           // backend changes among requests in [applied, ready), errors skipped
	hold      int           // requests in [ready, next applied or end)
}

func (tr transition) String() string {
	return fmt.Sprintf("g03 flip %s->%s status=%.2fs dataplane=%.2fs transition old=%d new=%d err=%d switches=%d hold=%d",
		tr.from, tr.to, tr.status.Seconds(), tr.dataplane.Seconds(), tr.old, tr.new, tr.errs, tr.switches, tr.hold)
}

// judge splits samples by marks and checks the G03 oracle. marks[0] is the
// flip that created the route; traffic starts after marks[0].ready. For each
// mark i the hold is [marks[i].ready, marks[i+1].applied), or up to end for
// the last mark, and for i > 0 the transition window is
// [marks[i].applied, marks[i].ready).
//
// It fails if any sample was sent before marks[0].ready, or if any hold has
// fewer than g03MinHold samples or a sample that isn't a 200 from marks[i].to.
// The error names the flip, the offset from ready, and what came back.
// Transition windows never fail; their counts go into the returned
// transitions, one per marks[1:], which are returned even when err != nil
// so the caller can log them.
//
// This is the pure function that defines G03 consistency.
func judge(samples []sample, marks []mark, end time.Time) ([]transition, error) {
	if len(marks) < 2 {
		return nil, fmt.Errorf("judge: got %d marks, want at least 2", len(marks))
	}
	for i := 1; i < len(samples); i++ {
		if samples[i].sent.Before(samples[i-1].sent) {
			return nil, fmt.Errorf("samples out of order at %s", samples[i].sent)
		}
	}
	var errs []error
	var early int
	for _, s := range samples {
		if s.sent.Before(marks[0].ready) {
			early++
		}
	}
	if early > 0 {
		errs = append(errs, fmt.Errorf("traffic started before the first flip was ready (%d requests)", early))
	}
	transitions := make([]transition, 0, len(marks)-1)
	for i, m := range marks {
		holdEnd := end
		if i+1 < len(marks) {
			holdEnd = marks[i+1].applied
		}
		from := ""
		if i > 0 {
			from = marks[i-1].to
		}
		hold := window(samples, m.ready, holdEnd)
		holdOK := true
		if len(hold) < g03MinHold {
			holdOK = false
			errs = append(errs, fmt.Errorf("flip %s: hold has %d requests, want at least %d", flipLabel(from, m.to), len(hold), g03MinHold))
		}
		var wrong int
		var bad sample
		var haveBad bool
		for _, s := range hold {
			if served(s, m.to) {
				continue
			}
			wrong++
			if !haveBad {
				bad = s
				haveBad = true
			}
		}
		if wrong > 0 {
			holdOK = false
			errs = append(errs, fmt.Errorf("flip %s: hold request at ready+%s got %s, want %q (hold had %d requests, %d wrong)",
				flipLabel(from, m.to), fmt.Sprintf("%.2fs", bad.sent.Sub(m.ready).Seconds()), observed(bad), m.to, len(hold), wrong))
		}
		if i == 0 {
			continue
		}
		tw := window(samples, m.applied, m.ready)
		tr := transition{
			from:   from,
			to:     m.to,
			status: m.status.Sub(m.applied),
			hold:   len(hold),
		}
		if holdOK {
			tr.dataplane = dataplaneDelay(samples, m.applied, holdEnd, m.to)
		} else {
			tr.dataplane = -time.Second
		}
		var prev string
		var seen bool
		for _, s := range tw {
			name, ok := responded(s)
			if !ok {
				tr.errs++
				continue
			}
			switch name {
			case tr.from:
				tr.old++
			case tr.to:
				tr.new++
			default:
				tr.errs++
			}
			if seen && prev != name {
				tr.switches++
			}
			prev = name
			seen = true
		}
		transitions = append(transitions, tr)
	}
	return transitions, errors.Join(errs...)
}

// tail renders the samples sent in the last d before end as backend run
// lengths, such as "blue×40 green×3 err(connection refused)×1". The traffic
// goroutine's cleanup logs it when the test failed before judge ran.
func tail(samples []sample, end time.Time, d time.Duration) string {
	start := end.Add(-d)
	var b strings.Builder
	var label string
	var n int
	flush := func() {
		if n == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s×%d", label, n)
	}
	for _, s := range samples {
		if s.sent.Before(start) || !s.sent.Before(end) {
			continue
		}
		next := runLabel(s)
		if n > 0 && next == label {
			n++
			continue
		}
		flush()
		label = next
		n = 1
	}
	flush()
	return b.String()
}

func window(samples []sample, start, end time.Time) []sample {
	var out []sample
	for _, s := range samples {
		if !s.sent.Before(start) && s.sent.Before(end) {
			out = append(out, s)
		}
	}
	return out
}

func served(s sample, backend string) bool {
	return s.err == "" && s.status == http.StatusOK && s.backend == backend
}

// responded reports a sample that names a backend. Transport errors and
// non-200s are skipped when counting switches.
func responded(s sample) (string, bool) {
	if s.err != "" || s.status != http.StatusOK || s.backend == "" {
		return "", false
	}
	return s.backend, true
}

func dataplaneDelay(samples []sample, applied, holdEnd time.Time, to string) time.Duration {
	w := window(samples, applied, holdEnd)
	i := len(w) - 1
	for i >= 0 && served(w[i], to) {
		i--
	}
	if i == len(w)-1 {
		return -time.Second
	}
	return w[i+1].sent.Sub(applied)
}

func flipLabel(from, to string) string {
	if from == "" {
		return "-> " + to
	}
	return from + "->" + to
}

func observed(s sample) string {
	if s.err != "" {
		return fmt.Sprintf("err %q", s.err)
	}
	if s.backend != "" {
		return fmt.Sprintf("%d from %q", s.status, s.backend)
	}
	return fmt.Sprintf("status %d", s.status)
}

func runLabel(s sample) string {
	if s.err != "" {
		return "err(" + s.err + ")"
	}
	if s.status != http.StatusOK || s.backend == "" {
		if s.status != http.StatusOK {
			return fmt.Sprintf("err(status %d)", s.status)
		}
		return "err"
	}
	return s.backend
}

// diffPath is the first field path where a and b differ, with map keys in
// sorted order. loadFlips puts it in the error so a fixture drift names the
// field (hostnames, backendRefs, ...).
func diffPath(a, b any) string {
	if p := diffAt(a, b, ""); p != "" {
		return p
	}
	return "<root>"
}

func diffAt(a, b any, path string) string {
	if reflect.DeepEqual(a, b) {
		return ""
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := mapKeys(am, bm)
		for _, k := range keys {
			next := k
			if path != "" {
				next = path + "." + k
			}
			if d := diffAt(am[k], bm[k], next); d != "" {
				return d
			}
		}
		if path == "" {
			return "<root>"
		}
		return path
	}
	as, aok := a.([]any)
	bs, bok := b.([]any)
	if aok && bok {
		n := len(as)
		if len(bs) > n {
			n = len(bs)
		}
		for i := 0; i < n; i++ {
			var av, bv any
			if i < len(as) {
				av = as[i]
			}
			if i < len(bs) {
				bv = bs[i]
			}
			next := fmt.Sprintf("%s[%d]", path, i)
			if d := diffAt(av, bv, next); d != "" {
				return d
			}
		}
		if path == "" {
			return "<root>"
		}
		return path
	}
	if path == "" {
		return "<root>"
	}
	return path
}

func mapKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestReadManifestsFindsPaymentsReplicas(t *testing.T) {
	objs, err := readManifests("testdata/g02")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range objs {
		if u.GetKind() != "Deployment" || u.GetName() != "payments" {
			continue
		}
		v, found, err := unstructured.NestedFieldNoCopy(u.Object, "spec", "replicas")
		n, ok := yamlInt(v)
		if err != nil || !found || !ok || n != 1 {
			t.Fatalf("spec.replicas = %#v (%T) found=%v err=%v", v, v, found, err)
		}
		return
	}
	t.Fatal("payments Deployment not found in testdata/g02")
}

func TestG03Fixtures(t *testing.T) {
	flips := g03Flips(t)
	var got []string
	for _, f := range flips {
		got = append(got, f.backend)
		wantDir := filepath.Join(g03RouteRoot, f.backend)
		if f.dir != wantDir {
			t.Fatalf("dir %q, want %q", f.dir, wantDir)
		}
	}
	if !slices.Equal(got, []string{"blue", "green", "blue"}) {
		t.Fatalf("flip order %v", got)
	}
}

func TestLoadFlipsRejects(t *testing.T) {
	cases := []struct {
		name  string
		order []string
		edit  func(t *testing.T, root string)
		want  []string
	}{
		{
			name:  "hostnames differ",
			order: []string{"blue", "green"},
			edit: func(t *testing.T, root string) {
				replaceIn(t, filepath.Join(root, "green", "route.yaml"), "hostnames: [shop.example]", "hostnames: [other.example]")
			},
			want: []string{"differ", "hostnames"},
		},
		{
			name:  "backendRefs reordered",
			order: []string{"blue", "green"},
			edit: func(t *testing.T, root string) {
				replaceIn(t, filepath.Join(root, "green", "route.yaml"),
					"        - {name: blue, port: 80, weight: 0}\n        - {name: green, port: 80, weight: 100}",
					"        - {name: green, port: 80, weight: 100}\n        - {name: blue, port: 80, weight: 0}")
			},
			want: []string{"differ"},
		},
		{
			name:  "extra Service",
			order: []string{"blue", "green"},
			edit: func(t *testing.T, root string) {
				body := "apiVersion: v1\nkind: Service\nmetadata:\n  name: extra\n  namespace: shop\n"
				if err := os.WriteFile(filepath.Join(root, "blue", "svc.yaml"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"exactly one object", "blue"},
		},
		{
			name:  "weights 50/50",
			order: []string{"blue", "green"},
			edit: func(t *testing.T, root string) {
				replaceIn(t, filepath.Join(root, "blue", "route.yaml"), "weight: 100", "weight: 50")
				replaceIn(t, filepath.Join(root, "blue", "route.yaml"), "weight: 0", "weight: 50")
			},
			want: []string{"weight 50", "blue"},
		},
		{
			name:  "weight 100 is not the directory name",
			order: []string{"blue", "green"},
			edit: func(t *testing.T, root string) {
				replaceIn(t, filepath.Join(root, "blue", "route.yaml"), "weight: 100", "weight: 0")
				replaceIn(t, filepath.Join(root, "blue", "route.yaml"), "{name: green, port: 80, weight: 0}", "{name: green, port: 80, weight: 100}")
			},
			want: []string{`selects "green", dir is blue`},
		},
		{
			name:  "consecutive repeat",
			order: []string{"blue", "blue"},
			want:  []string{"repeats"},
		},
		{
			name:  "weight field missing",
			order: []string{"blue", "green"},
			edit: func(t *testing.T, root string) {
				replaceIn(t, filepath.Join(root, "blue", "route.yaml"), "{name: green, port: 80, weight: 0}", "{name: green, port: 80}")
			},
			want: []string{"no weight", "blue"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			copyRoute(t, root, "blue")
			if !slices.Equal(tc.order, []string{"blue", "blue"}) {
				copyRoute(t, root, "green")
			}
			if tc.edit != nil {
				tc.edit(t, root)
			}
			_, err := loadFlips(root, tc.order)
			if err == nil {
				t.Fatal("loadFlips succeeded")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

func TestJudge(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ms := func(n int) time.Time { return base.Add(time.Duration(n) * time.Millisecond) }
	ok := func(at int, backend string) sample {
		return sample{sent: ms(at), status: http.StatusOK, backend: backend}
	}
	marks := []mark{
		{to: "blue", ready: ms(0)},
		{to: "green", applied: ms(3000), status: ms(4500), ready: ms(5000)},
	}
	end := ms(8000)
	baseline := fillSamples(ms(0), 20, 100*time.Millisecond, "blue")
	hold := fillSamples(ms(5100), 20, 100*time.Millisecond, "green")

	clean := append(append(append([]sample{}, baseline...), ok(3200, "blue"), ok(4000, "green")), hold...)

	beforeStatus := []mark{
		{to: "blue", ready: ms(0)},
		{to: "green", applied: ms(3000), status: ms(5000), ready: ms(5500)},
	}
	before := append(append(append([]sample{}, baseline...), ok(3100, "blue"), ok(3500, "green")),
		fillSamples(ms(5600), 20, 100*time.Millisecond, "green")...)

	badHold := append([]sample{}, clean...)
	// 5100 + 9*100ms = 6000, which is ready+1s.
	badHold[len(badHold)-20+9].backend = "blue"

	fiveHold := append([]sample{}, clean...)
	fiveHold[len(fiveHold)-20].status = 503
	fiveHold[len(fiveHold)-20].backend = ""

	errHold := append([]sample{}, clean...)
	errHold[len(errHold)-20].status = 0
	errHold[len(errHold)-20].backend = ""
	errHold[len(errHold)-20].err = "connection refused"

	thin := append(append([]sample{}, baseline...), ok(3200, "blue"), ok(4000, "green"))
	thin = append(thin, fillSamples(ms(5100), 4, 100*time.Millisecond, "green")...)

	flap := append(append([]sample{}, baseline...),
		ok(3100, "blue"), ok(3400, "green"), ok(3700, "blue"), ok(4000, "green"))
	flap = append(flap, hold...)

	transErr := append(append([]sample{}, baseline...),
		ok(3100, "blue"),
		sample{sent: ms(3500), status: 503},
		ok(4000, "green"))
	transErr = append(transErr, hold...)

	early := append([]sample{{sent: ms(-1), status: http.StatusOK, backend: "blue"}}, clean...)

	baseBad := append([]sample{}, clean...)
	baseBad[0].backend = "green"

	cases := []struct {
		name    string
		samples []sample
		marks   []mark
		end     time.Time
		wantErr []string
		check   func(t *testing.T, tr []transition)
	}{
		{
			name:    "clean flip",
			samples: clean,
			marks:   marks,
			end:     end,
			check: func(t *testing.T, tr []transition) {
				if len(tr) != 1 {
					t.Fatalf("transitions = %d, want 1", len(tr))
				}
				got := tr[0]
				if got.old != 1 || got.new != 1 || got.switches != 1 || got.errs != 0 || got.hold != 20 {
					t.Fatalf("transition old=%d new=%d err=%d switches=%d hold=%d", got.old, got.new, got.errs, got.switches, got.hold)
				}
				if got.dataplane != time.Second || got.status != 1500*time.Millisecond {
					t.Fatalf("status=%s dataplane=%s", got.status, got.dataplane)
				}
				want := "g03 flip blue->green status=1.50s dataplane=1.00s transition old=1 new=1 err=0 switches=1 hold=20"
				if got.String() != want {
					t.Fatalf("log line %q", got.String())
				}
			},
		},
		{
			name:    "dataplane flips before status",
			samples: before,
			marks:   beforeStatus,
			end:     ms(8000),
			check: func(t *testing.T, tr []transition) {
				if len(tr) != 1 {
					t.Fatalf("transitions = %d, want 1", len(tr))
				}
				if !(tr[0].dataplane < tr[0].status) || tr[0].dataplane != 500*time.Millisecond {
					t.Fatalf("dataplane=%s status=%s", tr[0].dataplane, tr[0].status)
				}
			},
		},
		{
			name:    "old backend during the hold",
			samples: badHold,
			marks:   marks,
			end:     end,
			wantErr: []string{`flip blue->green: hold request at ready+1.00s got 200 from "blue"`},
			check: func(t *testing.T, tr []transition) {
				if len(tr) != 1 || tr[0].dataplane != -time.Second {
					t.Fatalf("transitions %+v", tr)
				}
			},
		},
		{
			name:    "5xx in the hold",
			samples: fiveHold,
			marks:   marks,
			end:     end,
			wantErr: []string{"503"},
		},
		{
			name:    "transport error in the hold",
			samples: errHold,
			marks:   marks,
			end:     end,
			wantErr: []string{"connection refused"},
		},
		{
			name:    "hold with 4 samples",
			samples: thin,
			marks:   marks,
			end:     end,
			wantErr: []string{"hold has 4 requests, want at least 20"},
		},
		{
			name:    "flap in the transition window",
			samples: flap,
			marks:   marks,
			end:     end,
			check: func(t *testing.T, tr []transition) {
				if len(tr) != 1 || tr[0].switches != 3 {
					t.Fatalf("switches=%v", tr)
				}
			},
		},
		{
			name:    "5xx in the transition window",
			samples: transErr,
			marks:   marks,
			end:     end,
			check: func(t *testing.T, tr []transition) {
				if len(tr) != 1 || tr[0].errs != 1 {
					t.Fatalf("errs=%v", tr)
				}
			},
		},
		{
			name:    "sample before the first ready",
			samples: early,
			marks:   marks,
			end:     end,
			wantErr: []string{"traffic started before"},
			check: func(t *testing.T, tr []transition) {
				if len(tr) != 1 {
					t.Fatalf("transitions = %d, want 1", len(tr))
				}
			},
		},
		{
			name:    "baseline hold served the wrong backend",
			samples: baseBad,
			marks:   marks,
			end:     end,
			wantErr: []string{"-> blue"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := judge(tc.samples, tc.marks, tc.end)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("judge succeeded")
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q lacks %q", err, want)
					}
				}
			}
			if tc.check != nil {
				tc.check(t, tr)
			}
		})
	}
}

func TestTail(t *testing.T) {
	end := time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC)
	at := func(sec int, backend string) sample {
		return sample{sent: end.Add(time.Duration(sec) * time.Second), status: http.StatusOK, backend: backend}
	}
	samples := []sample{
		at(-9, "blue"),
		at(-4, "blue"), at(-3, "blue"), at(-2, "blue"),
		at(-1, "green"),
		{sent: end.Add(-500 * time.Millisecond), err: "connection refused"},
		at(0, "green"),
	}
	got := tail(samples, end, 5*time.Second)
	want := "blue×3 green×1 err(connection refused)×1"
	if got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
}

func TestReadBackend(t *testing.T) {
	ok := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Backend": []string{"blue"}},
		Body:       io.NopCloser(strings.NewReader(`{"backend":"blue","boot":"1"}`)),
	}
	got, err := readBackend(ok)
	if err != nil || got != "blue" {
		t.Fatalf("readBackend = %q, %v", got, err)
	}

	mismatch := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Backend": []string{"blue"}},
		Body:       io.NopCloser(strings.NewReader(`{"backend":"green"}`)),
	}
	if _, err := readBackend(mismatch); err == nil || !strings.Contains(err.Error(), "blue") || !strings.Contains(err.Error(), "green") {
		t.Fatalf("mismatch error %v", err)
	}

	status := &http.Response{
		StatusCode: 503,
		Header:     http.Header{"X-Backend": []string{"blue"}},
		Body:       io.NopCloser(strings.NewReader(`{"backend":"blue"}`)),
	}
	if _, err := readBackend(status); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("status error %v", err)
	}

	body := &countingReader{left: 2 << 20}
	huge := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body)}
	if _, err := readBackend(huge); err == nil {
		t.Fatal("huge body succeeded")
	}
	if body.n != 1<<20 {
		t.Fatalf("read %d bytes, want %d", body.n, 1<<20)
	}
}

type countingReader struct {
	left int
	n    int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.left {
		n = r.left
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.left -= n
	r.n += n
	return n, nil
}

func fillSamples(start time.Time, n int, step time.Duration, backend string) []sample {
	out := make([]sample, n)
	for i := 0; i < n; i++ {
		out[i] = sample{sent: start.Add(time.Duration(i) * step), status: http.StatusOK, backend: backend}
	}
	return out
}

func copyRoute(t *testing.T, root, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(g03RouteRoot, name, "route.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "route.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceIn(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(old)) {
		t.Fatalf("%s lacks %q", path, old)
	}
	next := bytes.Replace(data, []byte(old), []byte(new), 1)
	if err := os.WriteFile(path, next, 0o644); err != nil {
		t.Fatal(err)
	}
}
