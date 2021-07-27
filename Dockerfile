FROM golang:1.16-alpine as builder

WORKDIR /go/src/app

ADD go.mod /go/src/app
ADD go.sum /go/src/app
RUN go mod download

ADD . /go/src/app
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION} -X main.date=$(date '+%FT%T.%N%:z')" -o /go-mijia

FROM scratch

COPY --from=builder /go-mijia /go-mijia

ARG VERSION=dev
LABEL version="${VERSION}" maintainer="fopina <https://github.com/fopina/go-mijia/>"

ENTRYPOINT [ "/go-mijia" ]
