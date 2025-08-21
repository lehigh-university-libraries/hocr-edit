FROM golang:1.25-alpine3.22@sha256:f18a072054848d87a8077455f0ac8a25886f2397f88bfdd222d6fafbb5bba440

WORKDIR /app

RUN apk add --no-cache curl imagemagick && \
  adduser -S -G nobody -u 8888 hocr

COPY --chown=hocr:hocr main.go go.* ./
COPY --chown=hocr:hocr internal/ ./internal/
COPY --chown=hocr:hocr pkg/ ./pkg/

RUN go mod download && \
  go build -o /app/hocr && \
  go clean -cache -modcache

COPY --chown=hocr:hocr static/ ./static/

RUN mkdir uploads cache && \
  chown -R hocr uploads cache

USER hocr

ENTRYPOINT ["/app/hocr"]

HEALTHCHECK CMD curl -s http://localhost:8888/healthcheck
