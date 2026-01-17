
ARG APPNAME=tgze

# https://hub.docker.com/_/golang/tags
FROM golang:1.25-alpine AS build
ARG APPNAME
ENV APPNAME=$APPNAME
ENV CGO_ENABLED=0

ARG TARGETARCH

RUN mkdir -p /src/ffmpeg/
WORKDIR /src/ffmpeg/
RUN wget -O- https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-$TARGETARCH-static.tar.xz | tar -x -J
RUN mv ffmpeg-*-static/ffmpeg ffmpeg
RUN ls -l -a
RUN ./ffmpeg -version

RUN mkdir -p /$APPNAME/
WORKDIR /$APPNAME/
COPY *.go go.mod go.sum /$APPNAME/
RUN go version
RUN go get -v
RUN go build -o $APPNAME .
RUN ls -l -a



# https://hub.docker.com/_/alpine/tags
FROM alpine:3
ARG APPNAME
ENV APPNAME=$APPNAME
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2

RUN mkdir -p /$APPNAME/
WORKDIR /$APPNAME/
COPY --from=build /$APPNAME/ffmpeg/ffmpeg /bin/ffmpeg
COPY --from=build /$APPNAME/$APPNAME /$APPNAME/$APPNAME
ENTRYPOINT /$APPNAME/$APPNAME


