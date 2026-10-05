package tmux

import (
	"errors"
	"strings"
	"testing"

	"github.com/zigai/gotmux/internal/wire"
)

func readFixtureRequests(t *testing.T, kinds ...ObjectKind) []readRequest {
	t.Helper()

	requests := make([]readRequest, 0, len(kinds))
	for _, kind := range kinds {
		req, err := listRequest(kind, QueryOptions{Filter: "", ExtraFields: nil}, "")
		if err != nil {
			t.Fatal(err)
		}

		requests = append(requests, req)
	}

	return requests
}

func readFixtureRow(req readRequest) string {
	return string(wire.EncodeRecord(make([]string, len(req.fields))))
}

func readFixtureOutput(s *Server, parts ...string) []byte {
	return []byte(fixtureIdentityRecord(s) + strings.Join(parts, ""))
}

var readFixtureBoundary = string(wire.EncodeRecord(nil))

func TestDecodeReadAssignsRowsAcrossEmptyMiddleList(t *testing.T) {
	s := localServer(t)
	requests := readFixtureRequests(t, ObjectKindSession, ObjectKindClient, ObjectKindPane)

	data := readFixtureOutput(s, readFixtureRow(requests[0]), readFixtureBoundary, readFixtureBoundary, readFixtureRow(requests[2]), readFixtureRow(requests[2]))

	info, rows, err := s.decodeRead(data, true, requests)
	if err != nil {
		t.Fatal(err)
	}

	if got := []int{len(rows[0]), len(rows[1]), len(rows[2])}; got[0] != 1 || got[1] != 0 || got[2] != 2 {
		t.Fatalf("rows per request = %v, want [1 0 2]", got)
	}

	if !info.Identity.sameDaemon(fixtureIdentity(s)) {
		t.Fatalf("identity = %+v, want fixture daemon", info.Identity)
	}
}

func TestDecodeReadRejectsMalformedOutput(t *testing.T) {
	s := localServer(t)
	requests := readFixtureRequests(t, ObjectKindSession, ObjectKindPane)

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"truncated before boundary", readFixtureOutput(s, readFixtureRow(requests[0]))},
		{"extra boundary", readFixtureOutput(s, readFixtureRow(requests[0]), readFixtureBoundary, readFixtureRow(requests[1]), readFixtureBoundary)},
		{"row shaped for another request", readFixtureOutput(s, readFixtureRow(requests[1]), readFixtureBoundary)},
		{"missing identity record", []byte(readFixtureRow(requests[0]) + readFixtureBoundary)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.decodeRead(tc.data, true, requests); !errors.Is(err, wire.ErrRecord) {
				t.Fatalf("err = %v, want ErrRecord", err)
			}
		})
	}
}

func TestReadRejectsEmptyRequest(t *testing.T) {
	s := localServer(t)

	_, err := s.Read(t.Context(), ReadRequest{
		Sessions: UnavailableValue[QueryOptions](),
		Windows:  UnavailableValue[QueryOptions](),
		Panes:    UnavailableValue[QueryOptions](),
		Clients:  UnavailableValue[QueryOptions](),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
}
