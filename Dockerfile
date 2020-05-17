# Copyright (c) 2020 FEROX YT EIRL, www.ferox.yt <devops@ferox.yt>
# Copyright (c) 2020 Jérémy WALTHER <jeremy.walther@golflima.net>
# See <https://github.com/frxyt/gohrec> for details.

FROM golang:latest AS build
WORKDIR /app
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w -extldflags "-static"' -o gohrec .

FROM busybox:latest
LABEL maintainer="Jérémy WALTHER <jeremy@ferox.yt>"
WORKDIR /gohrec/log
COPY --from=build /app/gohrec /usr/local/bin/gohrec
RUN adduser -DHg gohrec gohrec
USER gohrec
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gohrec"]
CMD [ "record", "--listen=:8080" ]