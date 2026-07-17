/*
 * fileloader.c — BusyScout fast file transfer helper
 *
 * Usage: fileloader push <ip> <port> <filename>
 *        fileloader pull <ip> <port> <filename>
 *
 * Protocol (all multi-byte ints are big-endian):
 *
 * PUSH (BusyScout → device):
 *   [1B type=0x01] [4B namelen] [filename] [8B filesize] [data bytes...]
 *
 * PULL (device → BusyScout):
 *   loader reads file from device disk, sends:
 *     [1B type=0x02] [4B namelen] [filename]
 *     [1B type=0x03] [8B filesize] [data bytes...]
 *   On error (file not found):
 *     [1B type=0x04] [4B msglen] [error message]
 */

#define _GNU_SOURCE
#include <arpa/inet.h>
#include <fcntl.h>
#include <netdb.h>
#include <sys/socket.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <stdint.h>

#define TYPE_PUSH  0x01
#define TYPE_PULL  0x02
#define TYPE_DATA  0x03
#define TYPE_ERROR 0x04

static int connect_to(const char *ip, int port) {
    struct addrinfo hints, *res, *rp;
    char port_str[16];
    int sock = -1;

    snprintf(port_str, sizeof(port_str), "%d", port);

    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_INET;
    hints.ai_socktype = SOCK_STREAM;

    if (getaddrinfo(ip, port_str, &hints, &res) != 0) {
        perror("getaddrinfo");
        return -1;
    }

    for (rp = res; rp != NULL; rp = rp->ai_next) {
        sock = socket(rp->ai_family, rp->ai_socktype, rp->ai_protocol);
        if (sock < 0) continue;
        if (connect(sock, rp->ai_addr, rp->ai_addrlen) == 0) break;
        close(sock);
        sock = -1;
    }

    freeaddrinfo(res);

    if (sock < 0) {
        perror("connect");
        return -1;
    }

    return sock;
}

static int read_full(int fd, void *buf, size_t n) {
    size_t total = 0;
    while (total < n) {
        ssize_t r = read(fd, (char *)buf + total, n - total);
        if (r <= 0) return -1;
        total += (size_t)r;
    }
    return 0;
}

static int write_full(int fd, const void *buf, size_t n) {
    size_t total = 0;
    while (total < n) {
        ssize_t w = write(fd, (const char *)buf + total, n - total);
        if (w <= 0) return -1;
        total += (size_t)w;
    }
    return 0;
}

/* Write 4-byte big-endian uint32 */
static int write_u32(int fd, uint32_t v) {
    uint32_t nv = htonl(v);
    return write_full(fd, &nv, 4);
}

/* Read 4-byte big-endian uint32 */
static int read_u32(int fd, uint32_t *v) {
    uint32_t nv;
    if (read_full(fd, &nv, 4) < 0) return -1;
    *v = ntohl(nv);
    return 0;
}

/* Read 8-byte big-endian uint64 (for filesize) */
static int read_u64(int fd, uint64_t *v) {
    uint32_t hi, lo;
    if (read_u32(fd, &hi) < 0) return -1;
    if (read_u32(fd, &lo) < 0) return -1;
    *v = ((uint64_t)hi << 32) | lo;
    return 0;
}

/* Write 8-byte big-endian uint64 */
static int write_u64(int fd, uint64_t v) {
    uint32_t hi = (uint32_t)(v >> 32);
    uint32_t lo = (uint32_t)(v & 0xFFFFFFFF);
    if (write_u32(fd, hi) < 0) return -1;
    return write_u32(fd, lo);
}

static int do_push(int sock, const char *filename) {
    /* Read type byte */
    unsigned char type;
    if (read_full(sock, &type, 1) < 0) {
        fprintf(stderr, "read type failed\n");
        return 1;
    }
    if (type != TYPE_PUSH) {
        fprintf(stderr, "expected PUSH type (0x01), got 0x%02x\n", type);
        return 1;
    }

    /* Read filename (we already know it, but consume from stream) */
    uint32_t namelen;
    if (read_u32(sock, &namelen) < 0) {
        fprintf(stderr, "read namelen failed\n");
        return 1;
    }
    /* Skip filename bytes */
    char buf[4096];
    uint32_t remaining = namelen;
    while (remaining > 0) {
        uint32_t chunk = remaining > sizeof(buf) ? (uint32_t)sizeof(buf) : remaining;
        if (read_full(sock, buf, chunk) < 0) {
            fprintf(stderr, "read filename failed\n");
            return 1;
        }
        remaining -= chunk;
    }

    /* Read filesize */
    uint64_t filesize;
    if (read_u64(sock, &filesize) < 0) {
        fprintf(stderr, "read filesize failed\n");
        return 1;
    }

    /* Open output file */
    int fd = open(filename, O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        perror("open output");
        return 1;
    }

    /* Copy data */
    uint64_t copied = 0;
    while (copied < filesize) {
        uint64_t chunk = filesize - copied;
        if (chunk > sizeof(buf)) chunk = sizeof(buf);
        if (read_full(sock, buf, (size_t)chunk) < 0) {
            fprintf(stderr, "read data failed\n");
            close(fd);
            return 1;
        }
        if (write_full(fd, buf, (size_t)chunk) < 0) {
            perror("write output");
            close(fd);
            return 1;
        }
        copied += chunk;
    }

    close(fd);
    return 0;
}

static int do_pull(int sock, const char *filename) {
    /* Open and read the file from device's disk */
    int fd = open(filename, O_RDONLY);
    if (fd < 0) {
        /* Send error response */
        unsigned char err_type = TYPE_ERROR;
        write_full(sock, &err_type, 1);
        const char *errmsg = strerror(errno);
        uint32_t msglen = (uint32_t)strlen(errmsg);
        write_u32(sock, msglen);
        write_full(sock, errmsg, msglen);
        return 1;
    }

    /* Get file size */
    off_t sz = lseek(fd, 0, SEEK_END);
    if (sz < 0) {
        perror("lseek");
        close(fd);
        return 1;
    }
    lseek(fd, 0, SEEK_SET);
    uint64_t filesize = (uint64_t)sz;

    /* Send TYPE_PULL announcement: "I'm sending file X" */
    unsigned char type = TYPE_PULL;
    if (write_full(sock, &type, 1) < 0) {
        fprintf(stderr, "write type failed\n");
        close(fd);
        return 1;
    }

    uint32_t namelen = (uint32_t)strlen(filename);
    if (write_u32(sock, namelen) < 0) {
        fprintf(stderr, "write namelen failed\n");
        close(fd);
        return 1;
    }
    if (write_full(sock, filename, namelen) < 0) {
        fprintf(stderr, "write filename failed\n");
        close(fd);
        return 1;
    }

    /* Send TYPE_DATA with file contents */
    unsigned char data_type = TYPE_DATA;
    if (write_full(sock, &data_type, 1) < 0) {
        fprintf(stderr, "write data type failed\n");
        close(fd);
        return 1;
    }
    if (write_u64(sock, filesize) < 0) {
        fprintf(stderr, "write filesize failed\n");
        close(fd);
        return 1;
    }

    /* Send file data */
    char buf[4096];
    uint64_t sent = 0;
    while (sent < filesize) {
        ssize_t n = read(fd, buf, sizeof(buf));
        if (n <= 0) break;
        if (write_full(sock, buf, (size_t)n) < 0) {
            fprintf(stderr, "write data failed\n");
            close(fd);
            return 1;
        }
        sent += (uint64_t)n;
    }

    close(fd);
    return 0;
}

int main(int argc, char **argv) {
    if (argc < 4) {
        fprintf(stderr, "Usage: %s push|pull <ip> <port> [filename]\n", argv[0]);
        return 1;
    }

    const char *mode = argv[1];
    const char *ip = argv[2];
    int port = atoi(argv[3]);
    const char *filename = argc >= 5 ? argv[4] : NULL;

    if (strcmp(mode, "push") == 0 && !filename) {
        fprintf(stderr, "push requires filename\n");
        return 1;
    }
    if (strcmp(mode, "pull") == 0 && !filename) {
        fprintf(stderr, "pull requires filename\n");
        return 1;
    }

    // Daemonize: double-fork to survive shell exit / telnetd hangup.
    // BusyBox ash kills background jobs when the shell exits, so the
    // fileloader must detach from its parent session before the shell
    // terminates (which happens immediately after "&" returns).
    {
        pid_t pid = fork();
        if (pid < 0) { perror("fork"); return 1; }
        if (pid > 0) _exit(0);          // parent exits

        setsid();                        // new session, no controlling terminal

        pid = fork();
        if (pid < 0) { perror("fork2"); return 1; }
        if (pid > 0) _exit(0);          // first child exits, daemon is grandchild
    }

    // From here on we are fully detached from the original shell/telnet session.

    int sock = connect_to(ip, port);
    if (sock < 0) return 1;

    int rc;
    if (strcmp(mode, "push") == 0) {
        rc = do_push(sock, filename);
    } else if (strcmp(mode, "pull") == 0) {
        rc = do_pull(sock, filename);
    } else {
        fprintf(stderr, "unknown mode: %s\n", mode);
        rc = 1;
    }

    close(sock);
    return rc;
}
