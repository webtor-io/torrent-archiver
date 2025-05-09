FROM golang:latest as build

# set work dir
WORKDIR /app

# copy the source files
COPY . .

# disable crosscompiling
ENV CGO_ENABLED=0

# compile linux only
ENV GOOS=linux

# build the binary with debug information removed
RUN go build -ldflags '-w -s' -a -installsuffix cgo -o server

FROM alpine:3.21

# copy our static linked library
COPY --from=build /app/server .

# tell we are exposing our services
EXPOSE 8080 8081 8082 8083

# run it!
CMD ["./server"]
