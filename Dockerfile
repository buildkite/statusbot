# build stage
FROM golang:alpine AS build-env
WORKDIR  /go/src/github.com/buildkite/statusbot
ADD . .
RUN apk add --update --no-cache git
RUN go build -o statusbot

# final stage
FROM alpine
RUN apk add --update --no-cache ca-certificates
COPY --from=build-env /go/src/github.com/buildkite/statusbot/statusbot /
ENTRYPOINT ["/statusbot"]
