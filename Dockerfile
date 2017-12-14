FROM scratch
COPY /dist/statusbot-linux-amd64 /statusbot/
ENTRYPOINT ["/statusbot"]
