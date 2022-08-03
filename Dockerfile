# build app
FROM golang AS build-env

ADD . /Reptile

WORKDIR /Reptile

RUN CGO_ENABLED=0 go build .

# safe image
FROM alpine

ENV TZ=Asia/Shanghai

RUN apk update && apk add --no-cache ca-certificates && update-ca-certificates

COPY --from=build-env /Reptile/Reptile /usr/bin/Reptile
COPY --from=build-env /Reptile/assets/comic.ttf /assets/comic.ttf
COPY --from=build-env /Reptile/configs /configs

EXPOSE 8000

CMD ["Reptile"]

# HEALTHCHECK
HEALTHCHECK --interval=5s --timeout=3s  --retries=3  CMD ps -ef | grep Reptile || exit 1
