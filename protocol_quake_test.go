package gamedig

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hostsrc/go-gamedig/internal/quake"
)

func TestResultMarshalJSONOverrides(t *testing.T) {
	plain := Result{Name: "<a>", Raw: map[string]any{}, Players: []Player{}, Bots: []Player{}}
	type noMethod Result
	want, _ := json.Marshal(noMethod(plain))
	got, err := json.Marshal(plain)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("without overrides:\n got %s\nwant %s", got, want)
	}

	r := plain
	r.Password, r.MaxPlayers = true, 24
	r.nodeJSON = map[string]any{"password": "1", "maxplayers": "24"}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		t.Fatal(err)
	}
	const wantText = `{"name":"<a>","map":"","password":"1","raw":{},"version":"","maxplayers":"24","numplayers":0,` +
		`"players":[],"bots":[],"queryPort":0,"connect":"","ping":0}` + "\n"
	if buf.String() != wantText {
		t.Errorf("got  %s\nwant %s", buf.String(), wantText)
	}
	// json.Marshal still applies its default HTML escaping.
	if b, _ := json.Marshal(r); !strings.Contains(string(b), `\u003ca\u003e`) {
		t.Errorf("json.Marshal should escape HTML: %s", b)
	}
}

func TestQuakeResultFields(t *testing.T) {
	q := quake.Parse([]byte("\\sv_hostname\\x\\g_needpass\\1\\sv_maxclients\\24\\iv\\1\\clients\\3\n"), quake.Quake3, true)
	res := quakeResult(q, quake.Quake3, target{Host: "h", Port: 100})
	if !res.Password || res.MaxPlayers != 24 || res.NumPlayers != 3 {
		t.Errorf("typed fields: %+v", res)
	}
	q = quake.Parse([]byte("\\g_needpass\\0\\maxclients\\x\n"), quake.Quake3, true)
	if res := quakeResult(q, quake.Quake3, target{}); res.Password || res.MaxPlayers != 0 {
		t.Errorf("zero flags: %+v", res)
	}
	q = quake.Parse(nil, quake.Hexen2, true)
	if res := quakeResult(q, quake.Hexen2, target{Host: "h", Port: 26950}); res.Connect != "h:26900" {
		t.Errorf("hexen2 connect = %q", res.Connect)
	}
	if res := quakeResult(q, quake.Hexen2, target{Host: "h", Port: 50}); res.Connect != "" {
		t.Errorf("hexen2 port 0 should fall back to the query port, got %q", res.Connect)
	}
}

// FuzzQuakeResultJSON runs arbitrary reply bodies through parsing and
// conversion and requires the Result to marshal to valid JSON with
// node-gamedig's value types.
func FuzzQuakeResultJSON(f *testing.F) {
	f.Add([]byte("\\sv_hostname\\^1x\\g_needpass\\0\\sv_maxclients\\8\n1 2 \"p\" \"a\"\n0 0 \"b\"\n"), uint8(2), false)
	f.Add([]byte("\\hostname\\<x>\\*version\\2\\iv\\1\\clients\\4\n1 2 3 4 \"n\" \"s\" 1 2\n14 3\n"), uint8(0), true)
	f.Add([]byte("\n99999999999999999999999999 0x 1\n"), uint8(5), false)
	f.Fuzz(func(t *testing.T, body []byte, variant uint8, keep bool) {
		v := quake.Variant(int(variant) % 6)
		res := quakeResult(quake.Parse(body, v, !keep), v, target{Host: "h", Port: 27960})
		b, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("invalid JSON %s: %v", b, err)
		}
		for _, k := range []string{"password", "maxplayers", "numplayers"} {
			switch m[k].(type) {
			case bool, float64, string:
			default:
				t.Fatalf("%s has type %T", k, m[k])
			}
		}
		if len(m) != 12 {
			t.Fatalf("got %d top-level keys: %s", len(m), b)
		}
	})
}
