FROM alpine
RUN apk add --update --no-cache ca-certificates
COPY /dist/statusbot-linux-amd64 /statusbot
RUN chmod +x /statusbot
ENTRYPOINT ["/statusbot"]
