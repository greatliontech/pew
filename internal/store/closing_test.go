package store

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/greatliontech/pew/internal/recordingtest"
)

func TestPublicationCancellationKeepsTheCommittedDisposition(t *testing.T) {
	for _, count := range []int{1, 2} {
		for _, ending := range []string{"staged", "committed"} {
			t.Run(ending+string(rune('0'+count)), func(t *testing.T) {
				s := New(t.TempDir())
				var requests []WriteRequest
				before := make(map[string][]byte)
				for _, bench := range []string{"BenchmarkA", "BenchmarkB"}[:count] {
					if err := s.Write("", bench, "", recordingtest.Results(bench, []float64{1})); err != nil {
						t.Fatal(err)
					}
					data, err := s.ReadBytes(Key{Bench: bench})
					if err != nil {
						t.Fatal(err)
					}
					before[bench] = data
					requests = append(requests, WriteRequest{Bench: bench, Results: recordingtest.Results(bench, []float64{2})})
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				s.observePublication = func(stage string) {
					if stage == ending {
						cancel()
					}
				}
				err := s.WriteBatchContext(ctx, requests)
				if ending == "staged" && !errors.Is(err, context.Canceled) {
					t.Fatalf("pre-install cancellation: %v", err)
				}
				if ending == "committed" && err != nil {
					t.Fatalf("committed replacement reported lost: %v", err)
				}
				for _, request := range requests {
					data, err := s.ReadBytes(Key{Bench: request.Bench})
					if err != nil {
						t.Fatal(err)
					}
					if ending == "staged" && !bytes.Equal(data, before[request.Bench]) {
						t.Fatal("cancelled staging changed prior recording")
					}
					if ending == "committed" && bytes.Equal(data, before[request.Bench]) {
						t.Fatal("successful replacement did not land")
					}
				}
			})
		}
	}
}

func TestCASPublicationHonorsCancellationAfterStaging(t *testing.T) {
	s := New(t.TempDir())
	key := Key{Bench: "BenchmarkA"}
	if err := s.Write("", key.Bench, "", recordingtest.Results("A", []float64{1})); err != nil {
		t.Fatal(err)
	}
	prior, err := s.ReadBytes(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.observePublication = func(stage string) {
		if stage == "staged" {
			cancel()
		}
	}
	if err := s.ReplaceContext(ctx, key, prior, recordingtest.Results("A", []float64{2})); !errors.Is(err, context.Canceled) {
		t.Fatalf("CAS closing cancellation: %v", err)
	}
	got, err := s.ReadBytes(key)
	if err != nil || !bytes.Equal(got, prior) {
		t.Fatalf("cancelled CAS changed prior bytes: %v", err)
	}
}
