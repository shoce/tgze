
ARG APPNAME=tgze

# https://hub.docker.com/_/golang/tags
FROM golang:1.25.0-alpine3.22 AS build
ARG APPNAME
ENV APPNAME=$APPNAME

ENV CGO_ENABLED=0

#ARG TARGETARCH
#
#RUN apt update
#RUN apt -y -q install xz-utils
#
#RUN mkdir -p /src/ffmpeg/
#WORKDIR /src/ffmpeg/
#RUN curl -s -S -L -O https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-$TARGETARCH-static.tar.xz
#RUN tar -x -J -f ffmpeg-release-$TARGETARCH-static.tar.xz
#RUN mv ffmpeg-*-static/ffmpeg ffmpeg
#RUN ls -l -a
#RUN ./ffmpeg -version

RUN mkdir -p /src/$APPNAME/
WORKDIR /src/$APPNAME/

COPY *.go go.mod go.sum /src/$APPNAME/
RUN go version
RUN go get -v
RUN go build -o $APPNAME .
RUN ls -l -a



# https://hub.docker.com/_/alpine/tags
FROM alpine:3.22
ARG APPNAME
ENV APPNAME=$APPNAME
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2

#COPY --from=build /src/ffmpeg/ffmpeg /bin/ffmpeg
COPY --from=build /src/$APPNAME/$APPNAME /bin/$APPNAME

RUN mkdir -p /opt/$APPNAME/
WORKDIR /opt/$APPNAME/
ENTRYPOINT /bin/$APPNAME


