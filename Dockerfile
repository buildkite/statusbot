FROM scratch
COPY /dist/statusbot-linux-amd64 /statusbot
RUN chmod +x /statusbot
ENTRYPOINT ["/statusbot"]
