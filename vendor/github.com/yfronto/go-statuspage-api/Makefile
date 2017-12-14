

.PHONY: build
build: tools componentstatus_string.go
	@go build

.PHONY: install
install: tools componentstatus_string.go
	@govendor install +vendor,^program

.PHONY: tools
tools:
	@go get -u github.com/kardianos/govendor

.PHONY: sync
sync:
	@govendor sync

componentstatus_string.go:
	@stringer -type=ComponentStatus
