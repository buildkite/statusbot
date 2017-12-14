FROM golang:1.9.2 AS build-env

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /go/src/github.com/buildkitestatusbot/
ADD . /go/src/github.com/buildkitestatusbot/
RUN go build -a -tags netgo -ldflags '-w' -o /binstatusbot/

FROM scratch
COPY --from=build-env /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build-env /bin/statusbot statusbot/
ENTRYPOINT ["/statusbot"]
