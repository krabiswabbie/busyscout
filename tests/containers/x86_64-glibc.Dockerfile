# x86_64 telnetd for BusyScout integration tests
# Uses Alpine (musl) + gcompat for glibc compatibility with fileloader-x86_64-glibc
FROM --platform=linux/amd64 alpine:3.20

RUN apk add --no-cache busybox-extras gcompat

RUN adduser -D user && echo "user:password" | chpasswd

EXPOSE 23
CMD ["telnetd", "-F", "-l", "/bin/login"]
