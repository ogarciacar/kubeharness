package inference_test

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type model struct {
	alias string
	id    string
}

var (
	qwen  = model{alias: "qwen", id: "unsloth/Qwen3-0.6B-Q8_0"}
	smol  = model{alias: "smol", id: "bartowski/SmolLM2-135M-Instruct-Q8_0"}
	gemma = model{alias: "gemma", id: "ggml-org/gemma-3-270m-it-Q8_0"}

	models = []model{qwen, smol, gemma}
)

// label is the model_id kronk puts on chat_requests_total, which is the GGUF
// file name, so the id without its org.
func (m model) label() string {
	_, file, _ := strings.Cut(m.id, "/")
	return file
}

// kronk exports no series for a model before its first chat, so a missing
// label must read as zero.
type served map[string]float64

func parseServed(metrics string) (served, error) {
	out := served{}
	found := false
	for line := range strings.Lines(metrics) {
		rest, ok := strings.CutPrefix(line, "chat_requests_total{")
		if !ok {
			continue
		}
		set, value, ok := strings.Cut(rest, "} ")
		if !ok {
			return nil, fmt.Errorf("chat_requests_total line %q has no value", line)
		}
		labels := map[string]string{}
		for pair := range strings.SplitSeq(set, ",") {
			name, quoted, _ := strings.Cut(pair, "=")
			v, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, fmt.Errorf("chat_requests_total{%s}: label %s: %w", set, name, err)
			}
			labels[name] = v
		}
		id, ok := labels["model_id"]
		if !ok {
			return nil, fmt.Errorf("chat_requests_total{%s} has no model_id", set)
		}
		found = true
		if labels["status"] != "ok" {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("chat_requests_total{%s}: %w", set, err)
		}
		out[id] = n
	}
	if !found {
		return nil, errors.New("no chat_requests_total series in the scrape")
	}
	return out, nil
}

func grewSince(before, now served) []string {
	var grew []string
	for id, n := range now {
		if n > before[id] {
			grew = append(grew, id)
		}
	}
	slices.Sort(grew)
	return grew
}

type observed struct {
	status int
	model  string
	grew   []string
}

func (o observed) String() string {
	return fmt.Sprintf("%d naming %q with kronk serving %v", o.status, o.model, o.grew)
}

func (o observed) answeredBy(want model) error {
	if o.status != http.StatusOK || o.model != want.id || !slices.Equal(o.grew, []string{want.label()}) {
		return fmt.Errorf("want %s to answer with 200 naming %q and kronk serving [%s], got %v", want.alias, want.id, want.label(), o)
	}
	return nil
}

func (o observed) refused() error {
	if o.status != http.StatusNotFound || len(o.grew) > 0 {
		return fmt.Errorf("got %v, want 404 with kronk serving nothing", o)
	}
	return nil
}

func (o observed) crossed(asked, got model) error {
	if err := o.answeredBy(got); err != nil {
		return fmt.Errorf("asked %s, want the crossed route to reach %s: %w", asked.alias, got.id, err)
	}
	if o.answeredBy(asked) == nil {
		return fmt.Errorf("asked %s and got %s, but the %s check accepted %v", asked.alias, got.id, asked.alias, o)
	}
	return nil
}

func TestObservedJudges(t *testing.T) {
	judges := map[string]func(observed) error{
		"answeredBy(qwen)":    func(o observed) error { return o.answeredBy(qwen) },
		"answeredBy(smol)":    func(o observed) error { return o.answeredBy(smol) },
		"answeredBy(gemma)":   func(o observed) error { return o.answeredBy(gemma) },
		"refused":             observed.refused,
		"crossed(smol, qwen)": func(o observed) error { return o.crossed(smol, qwen) },
		"crossed(qwen, qwen)": func(o observed) error { return o.crossed(qwen, qwen) },
	}
	cases := []struct {
		name string
		o    observed
		pass []string
	}{
		{"qwen answered", observed{200, "unsloth/Qwen3-0.6B-Q8_0", []string{"Qwen3-0.6B-Q8_0"}}, []string{"answeredBy(qwen)", "crossed(smol, qwen)"}},
		{"smol answered", observed{200, "bartowski/SmolLM2-135M-Instruct-Q8_0", []string{"SmolLM2-135M-Instruct-Q8_0"}}, []string{"answeredBy(smol)"}},
		{"gemma answered", observed{200, "ggml-org/gemma-3-270m-it-Q8_0", []string{"gemma-3-270m-it-Q8_0"}}, []string{"answeredBy(gemma)"}},
		{"reply names smol but kronk served qwen", observed{200, "bartowski/SmolLM2-135M-Instruct-Q8_0", []string{"Qwen3-0.6B-Q8_0"}}, nil},
		{"backend without model override", observed{400, "", nil}, nil},
		{"unknown alias", observed{404, "", nil}, []string{"refused"}},
		{"unknown alias that kronk still served", observed{404, "", []string{"Qwen3-0.6B-Q8_0"}}, nil},
		{"gateway reply naming qwen while kronk served nothing", observed{200, "unsloth/Qwen3-0.6B-Q8_0", nil}, nil},
		{"server error naming qwen after kronk served qwen", observed{500, "unsloth/Qwen3-0.6B-Q8_0", []string{"Qwen3-0.6B-Q8_0"}}, nil},
		{"reply names qwen but kronk served qwen and smol", observed{200, "unsloth/Qwen3-0.6B-Q8_0", []string{"Qwen3-0.6B-Q8_0", "SmolLM2-135M-Instruct-Q8_0"}}, nil},
		{"reply names smol but kronk served qwen and smol", observed{200, "bartowski/SmolLM2-135M-Instruct-Q8_0", []string{"Qwen3-0.6B-Q8_0", "SmolLM2-135M-Instruct-Q8_0"}}, nil},
		{"not found naming qwen after kronk served qwen", observed{404, "unsloth/Qwen3-0.6B-Q8_0", []string{"Qwen3-0.6B-Q8_0"}}, nil},
		{"bad request naming qwen after kronk served qwen", observed{400, "unsloth/Qwen3-0.6B-Q8_0", []string{"Qwen3-0.6B-Q8_0"}}, nil},
		{"created naming qwen after kronk served qwen", observed{201, "unsloth/Qwen3-0.6B-Q8_0", []string{"Qwen3-0.6B-Q8_0"}}, nil},
	}
	for _, c := range cases {
		for name, judge := range judges {
			err := judge(c.o)
			if want := slices.Contains(c.pass, name); (err == nil) != want {
				t.Errorf("%s: %s returned %v, want pass=%t", c.name, name, err, want)
			}
		}
	}
	for _, r := range []struct {
		m     model
		label string
	}{
		{qwen, "Qwen3-0.6B-Q8_0"},
		{smol, "SmolLM2-135M-Instruct-Q8_0"},
		{gemma, "gemma-3-270m-it-Q8_0"},
	} {
		o := observed{200, r.m.id, []string{r.label}}
		for _, other := range models {
			if err := o.answeredBy(other); (err == nil) != (other == r.m) {
				t.Errorf("%s answered: answeredBy(%s) returned %v", r.m.alias, other.alias, err)
			}
		}
	}
}

func TestCrossedMessage(t *testing.T) {
	err := observed{200, "bartowski/SmolLM2-135M-Instruct-Q8_0", []string{"SmolLM2-135M-Instruct-Q8_0"}}.crossed(smol, qwen)
	if err == nil {
		t.Fatal("crossed(smol, qwen) accepted a smol reply")
	}
	msg := err.Error()
	for _, want := range []string{"asked smol", "reach unsloth/Qwen3-0.6B-Q8_0:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("crossed(smol, qwen) error %q lacks %q", msg, want)
		}
	}
	for _, unwanted := range []string{"/m/", "reach bartowski"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("crossed(smol, qwen) error %q contains %q", msg, unwanted)
		}
	}
}

func TestServedGrowth(t *testing.T) {
	before := `# HELP chat_requests_total Total chat completion requests by model_id and status (ok|error|cancel).
# TYPE chat_requests_total counter
chat_requests_total{model_id="Qwen3-0.6B-Q8_0",status="ok"} 1
chat_request_duration_seconds_count{model_id="Qwen3-0.6B-Q8_0"} 1
`
	now := before + `chat_requests_total{model_id="SmolLM2-135M-Instruct-Q8_0",status="ok"} 1
chat_requests_total{model_id="gemma-3-270m-it-Q8_0",status="error"} 4
`
	b, err := parseServed(before)
	if err != nil {
		t.Fatal(err)
	}
	n, err := parseServed(now)
	if err != nil {
		t.Fatal(err)
	}
	if got := grewSince(b, n); !slices.Equal(got, []string{"SmolLM2-135M-Instruct-Q8_0"}) {
		t.Errorf("grewSince = %v, want only the smol series that appeared", got)
	}
	if got := grewSince(n, n); len(got) != 0 {
		t.Errorf("grewSince with no new chats = %v, want none", got)
	}
	if got, err := parseServed(`chat_requests_total{model_id="x",xstatus="ok"} 3
`); err != nil || got["x"] != 0 {
		t.Errorf("parseServed(xstatus=\"ok\") = %v, %v, want x not counted as ok", got, err)
	}
	for name, page := range map[string]string{
		"non-numeric count":                    `chat_requests_total{model_id="x",status="ok"} NaNx`,
		"empty page":                           "",
		"no chat series":                       "# TYPE chat_requests_total counter\nchat_request_duration_seconds_count{model_id=\"x\"} 1\n",
		"series with no id":                    `chat_requests_total{status="ok"} 1`,
		"valid series and a series with no id": "chat_requests_total{model_id=\"y\",status=\"ok\"} 1\nchat_requests_total{status=\"ok\"} 1\n",
		"series with no value":                 "chat_requests_total{model_id=\"y\",status=\"ok\"} 1\nchat_requests_total{model_id=\"x\",status=\"ok\"}\n",
	} {
		if _, err := parseServed(page); err == nil {
			t.Errorf("parseServed accepted a page with %s", name)
		}
	}
}
