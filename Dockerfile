FROM golang:latest as build

# set work dir
WORKDIR /app

# copy the source files
COPY . .

# enable modules
ENV GO111MODULE=on

# disable crosscompiling
ENV CGO_ENABLED=0

# compile linux only
ENV GOOS=linux

# build the binary with debug information removed
RUN go build -mod=vendor -ldflags '-w -s' -a -installsuffix cgo -o server

FROM scratch

# copy our static linked library
COPY --from=build /app/server .

# tell we are exposing our service on port 8080, 8081
EXPOSE 8080 8081

# run it!
CMD ["./server"]
