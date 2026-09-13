FROM alpine:3.20

RUN apk --no-cache add ca-certificates curl

WORKDIR /
COPY bin/metrics-collector /bin/metrics-collector

USER 65532:65532

ENTRYPOINT ["/bin/metrics-collector"]
