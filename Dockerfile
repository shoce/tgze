

# https://hub.docker.com/_/golang/tags
FROM golang:1.23.2 AS build

#ARG TARGETARCH
#
#RUN apt update
#RUN apt -y -q install xz-utils
#
#RUN mkdir -p /root/ffmpeg/
#WORKDIR /root/ffmpeg/
#RUN curl -s -S -L -O https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-$TARGETARCH-static.tar.xz
#RUN tar -x -J -f ffmpeg-release-$TARGETARCH-static.tar.xz
#RUN mv ffmpeg-*-static/ffmpeg ffmpeg
#RUN ls -l -a
#RUN ./ffmpeg -version

RUN mkdir -p /root/tgzebot/
WORKDIR /root/tgzebot/
COPY tgzebot.go go.mod go.sum /root/tgzebot/
RUN go version
RUN go get -v
RUN go build -o tgzebot tgzebot.go
RUN ls -l -a



# https://hub.docker.com/_/alpine/tags
FROM alpine:3.20.3
RUN apk add --no-cache tzdata
RUN apk add --no-cache gcompat && ln -s -f -v ld-linux-x86-64.so.2 /lib/libresolv.so.2

#COPY --from=build /root/ffmpeg/ffmpeg /bin/
COPY --from=build /root/tgzebot/tgzebot /bin/

WORKDIR /root/
ENTRYPOINT ["/bin/tgzebot"]


