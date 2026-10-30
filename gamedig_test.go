package gamedig

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// Expected strings are JavaScript's JSON.stringify output.
func TestNumberMarshalMatchesJavaScript(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{1e6, "1000000"}, {1e21, "1e+21"}, {1e20, "100000000000000000000"},
		{1e-7, "1e-7"}, {1e-6, "0.000001"}, {0.1, "0.1"},
		{float64(float32(0.1)), "0.10000000149011612"}, {math.Copysign(0, -1), "0"},
		{123456789.5, "123456789.5"}, {5e-324, "5e-324"}, {-2.5e-8, "-2.5e-8"},
		{math.MaxFloat64, "1.7976931348623157e+308"}, {-1, "-1"}, {99999, "99999"},
		{math.NaN(), "null"}, {math.Inf(1), "null"}, {math.Inf(-1), "null"},
	}
	for _, tt := range tests {
		b, err := json.Marshal(Number(tt.in))
		if err != nil || string(b) != tt.want {
			t.Errorf("Number(%v) = %s, %v; want %s", tt.in, b, err, tt.want)
		}
	}
}

func TestAttemptPorts(t *testing.T) {
	game := func(id string) Game {
		g, err := Lookup(id, false)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	tests := []struct {
		name string
		game Game
		opts Options
		want []int
	}{
		{"given port tried twice like node", game("counterstrike16"), Options{Port: 27016}, []int{27016, 27016}},
		{"given port only", game("counterstrike16"), Options{Port: 27016, GivenPortOnly: true}, []int{27016}},
		{"offsets", game("vrising"), Options{Port: 9876}, []int{9877, 9891, 9876}},
		{"catalog default", game("ets2"), Options{}, []int{27016}},
		{"protocol default", Game{Protocol: "valve"}, Options{}, []int{0}},
	}
	for _, tt := range tests {
		got := attemptPorts(tt.game, tt.opts)
		if !equalInts(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestQueryErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Query(ctx, Options{Host: "127.0.0.1"}); !errors.Is(err, ErrNoGame) {
		t.Errorf("no type: %v", err)
	}
	if _, err := Query(ctx, Options{Type: "nope", Host: "127.0.0.1"}); !errors.Is(err, ErrUnknownGame) || err.Error() != "Invalid game: nope" {
		t.Errorf("unknown game: %v", err)
	}
	if _, err := Query(ctx, Options{Type: "minecraft", Host: "127.0.0.1"}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unsupported: %v", err)
	}
	if _, err := Query(ctx, Options{Type: "counterstrike16"}); !errors.Is(err, ErrNoHost) {
		t.Errorf("no host: %v", err)
	}
}

func TestProtocolDefaultPortAndRetryCap(t *testing.T) {
	opts := Options{Type: "protocol-valve", Host: "127.0.0.1", MaxRetries: 1 << 30,
		SocketTimeout: 5 * time.Millisecond, AttemptTimeout: 50 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Query(ctx, opts)
	var ae *AttemptsError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want *AttemptsError", err)
	}
	if len(ae.Errors) != maxRetries {
		t.Errorf("%d attempts, want the cap %d", len(ae.Errors), maxRetries)
	}
	if !strings.Contains(ae.Errors[0].Error(), "port=27015") {
		t.Errorf("first attempt %v, want port 27015", ae.Errors[0])
	}
	if ae.Summary() != "Failed all 100 attempts" || !strings.HasPrefix(err.Error(), "Failed all 100 attempts: attempt #1") {
		t.Errorf("messages: %q / %q", ae.Summary(), err.Error())
	}
}
