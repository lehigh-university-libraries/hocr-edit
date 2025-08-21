FROM golang:1.25-alpine3.22@sha256:f18a072054848d87a8077455f0ac8a25886f2397f88bfdd222d6fafbb5bba440

SHELL ["/bin/ash", "-o", "pipefail", "-c"]


ARG \
  # renovate: datasource=github-releases depName=gosu packageName=tianon/gosu
  GOSU_VERSION=1.17

# install gosu
RUN apk add --no-cache --virtual .gosu-deps \
    ca-certificates \
    dpkg \
    gnupg && \
	dpkgArch="$(dpkg --print-architecture | awk -F- '{ print $NF }')" && \
	wget -q -O /usr/local/bin/gosu "https://github.com/tianon/gosu/releases/download/$GOSU_VERSION/gosu-$dpkgArch" && \
	wget -q -O /usr/local/bin/gosu.asc "https://github.com/tianon/gosu/releases/download/$GOSU_VERSION/gosu-$dpkgArch.asc" && \
	GNUPGHOME="$(mktemp -d)" && \
	export GNUPGHOME && \
	gpg --batch --keyserver hkps://keys.openpgp.org --recv-keys B42F6819007F00F88E364FD4036A9C25BF357DD4 && \
	gpg --batch --verify /usr/local/bin/gosu.asc /usr/local/bin/gosu && \
	gpgconf --kill all && \
	rm -rf "$GNUPGHOME" /usr/local/bin/gosu.asc && \
	apk del --no-network .gosu-deps && \
	chmod +x /usr/local/bin/gosu

WORKDIR /app

RUN apk add --no-cache \
    bash \
    ca-certificates \
    curl \
    imagemagick  && \
  adduser -S -G nobody -u 8888 hocr

COPY --chown=hocr:hocr main.go go.* docker-entrypoint.sh ./
COPY --chown=hocr:hocr internal/ ./internal/
COPY --chown=hocr:hocr pkg/ ./pkg/

RUN go mod download && \
  go build -o /app/hocr && \
  go clean -cache -modcache

COPY --chown=hocr:hocr static/ ./static/

RUN mkdir uploads cache && \
  chown -R hocr uploads cache


ENTRYPOINT ["/bin/bash"]
CMD ["/app/docker-entrypoint.sh"]

HEALTHCHECK CMD curl -s http://localhost:8888/healthcheck
