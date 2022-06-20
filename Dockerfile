# build app
FROM golang AS build-env

ADD . /qingshanyoufeng

WORKDIR /qingshanyoufeng

RUN CGO_ENABLED=0 go build .

# safe image
FROM alpine

ENV TZ=Asia/Shanghai

RUN apk update && apk add --no-cache ca-certificates && update-ca-certificates

COPY --from=build-env /qingshanyoufeng/qingshanyoufeng /usr/bin/qingshanyoufeng
COPY --from=build-env /qingshanyoufeng/assets/comic.ttf /assets/comic.ttf
COPY --from=build-env /qingshanyoufeng/configs /configs

EXPOSE 8000

CMD ["qingshanyoufeng"]

# HEALTHCHECK
HEALTHCHECK --interval=5s --timeout=3s  --retries=3  CMD ps -ef | grep qingshanyoufeng || exit 1
