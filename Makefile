.PHONY: test race integration vet generate check-generated check-ledger release-check consumer build

test:
	GOWORK=off go test ./...
race:
	GOWORK=off go test -race ./...
integration:
	TMUX_INTEGRATION_REQUIRED=1 GOWORK=off go test -tags=integration -count=1 ./...
vet:
	GOWORK=off go vet ./...
generate:
	python3 internal/schema/generate.py
check-generated:
	python3 internal/schema/generate.py --check
check-ledger:
	python3 scripts/check_ledger.py
release-check:
	python3 scripts/check_release.py
consumer:
	python3 scripts/test_consumer.py
build:
	CGO_ENABLED=0 GOWORK=off go build ./...
