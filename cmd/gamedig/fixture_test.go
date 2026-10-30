package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/hostsrc/go-gamedig/internal/fixture"
)

// TestFixtures replays every recorded node-gamedig query and requires the
// same requests on the wire and the same JSON output. Set
// GAMEDIG_FIXTURES to a directory to replay extra recordings as well, such
// as private captures of live servers.
func TestFixtures(t *testing.T) {
	paths, _ := filepath.Glob("../../testdata/*.json")
	if dir := os.Getenv("GAMEDIG_FIXTURES"); dir != "" {
		extra, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		paths = append(paths, extra...)
	}
	if len(paths) == 0 {
		t.Fatal("no fixtures found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			f, err := fixture.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			srv, err := fixture.Serve(f.Exchanges)
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()

			args := append(append([]string{}, f.Args...), "--givenPortOnly", "--port="+strconv.Itoa(srv.Port()), "127.0.0.1")
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 0 {
				t.Fatalf("exit %d: %s", code, stderr.String())
			}

			var got map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("output %q: %v", stdout.String(), err)
			}
			gotJSON, _ := json.Marshal(fixture.Normalize(got, srv.Port()))
			var gotV, wantV any
			_ = json.Unmarshal(gotJSON, &gotV)
			if err := json.Unmarshal(f.Expected, &wantV); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotV, wantV) {
				t.Errorf("output differs from node-gamedig %s\n got: %s\nwant: %s", f.GamedigVersion, gotJSON, f.Expected)
			}
			srv.Close()
			if u := srv.Unmatched(); len(u) > 0 {
				t.Errorf("sent %d request(s) node-gamedig did not, first %x", len(u), u[0])
			}
			if u := srv.Unused(); len(u) > 0 {
				t.Errorf("never sent the request of exchange(s) %v, which node-gamedig sent", u)
			}
		})
	}
}

func TestArgsLikeMinimist(t *testing.T) {
	flags, pos := parseArgs([]string{
		"--type=counterstrike16", "--requestRules=1", "--givenPortOnly=false", "--pretty", "true",
		"--maxAttempts", "1", "--port=27015abc", "--no-requestPlayers", "1.2.3.4",
	})
	opts, err := buildOptions(flags, pos)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.RequestRules || opts.GivenPortOnly || !opts.SkipPlayers || opts.Port != 27015 || opts.Host != "1.2.3.4" {
		t.Errorf("opts = %+v", opts)
	}
	if flags["pretty"] != "true" || flags["maxAttempts"] != "1" {
		t.Errorf("flags = %v", flags)
	}

	flags, pos = parseArgs([]string{"--type=counterstrike16", "--port=abc", "1.2.3.4:27016"})
	if opts, _ = buildOptions(flags, pos); opts.Port != 27016 {
		t.Errorf("target port should win: %d", opts.Port)
	}
	flags, pos = parseArgs([]string{"--type=counterstrike16", "--port=abc", "1.2.3.4"})
	if opts, _ = buildOptions(flags, pos); opts.Port != 0 {
		t.Errorf("unparsable port should be unset: %d", opts.Port)
	}
}

func TestErrorOutputIsNodeMessage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run([]string{"--type=counterstrike16", "--socketTimeout=5", "--attemptTimeout=50", "--port=9", "--givenPortOnly", "127.0.0.1"}, &stdout, &stderr)
	if got := stdout.String(); got != "{\"error\":\"Failed all 1 attempts\"}\n" {
		t.Errorf("stdout = %q", got)
	}
	if code := run([]string{"--strict", "--type=nope", "127.0.0.1"}, &stdout, &stderr); code != 1 {
		t.Errorf("--strict exit = %d", code)
	}
}
