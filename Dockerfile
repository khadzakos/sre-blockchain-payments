FROM golang:1.26.1-alpine3.23 AS tooling
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM tooling AS build
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/payments ./cmd/payments

FROM alpine:3.23.3 AS runtime
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=build /out/payments /usr/local/bin/payments
USER app
EXPOSE 8080
ENTRYPOINT ["payments"]
CMD ["api"]
