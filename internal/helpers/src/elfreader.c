// internal/helpers/elfreader.c
// Tiny static ELF reader — reads /bin/busybox header and prints arch info.
// Build: CC=arm-linux-gnueabi-gcc -static -s -Os -o elfreader-arm elfreader.c
//        strip elfreader-arm

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <elf.h>

static void print_field(const char *key, const char *value) {
    printf("%s=%s\n", key, value);
}

int main(int argc, char **argv) {
    const char *path = "/bin/busybox";
    if (argc > 1) path = argv[1];

    int fd = open(path, O_RDONLY);
    if (fd < 0) { perror("open"); return 1; }

    // Read first 64 bytes (covers both 32-bit and 64-bit ELF headers)
    unsigned char buf[64];
    ssize_t n = read(fd, buf, sizeof(buf));
    close(fd);
    if (n < 20) { fprintf(stderr, "short read\n"); return 1; }

    // Check ELF magic
    if (buf[0] != 0x7f || buf[1] != 'E' || buf[2] != 'L' || buf[3] != 'F') {
        fprintf(stderr, "not an ELF file\n");
        return 1;
    }

    // EI_CLASS
    if (buf[4] == 1) print_field("class", "32");
    else if (buf[4] == 2) print_field("class", "64");

    // EI_DATA
    if (buf[5] == 1) print_field("endian", "little");
    else if (buf[5] == 2) print_field("endian", "big");

    // e_machine (bytes 18-19)
    unsigned short machine = buf[18] | (buf[19] << 8);
    char machine_str[16];
    snprintf(machine_str, sizeof(machine_str), "%u", machine);
    print_field("machine", machine_str);

    // ARM attributes section: parse section headers for .ARM.attributes
    // We need e_shoff to find section header table
    unsigned long shoff;
    unsigned short shentsize, shnum, shstrndx;

    if (buf[4] == 1) { // 32-bit
        shoff     = *(unsigned int *)(buf + 32);
        shentsize = *(unsigned short *)(buf + 46);
        shnum     = *(unsigned short *)(buf + 48);
        shstrndx  = *(unsigned short *)(buf + 50);
    } else { // 64-bit
        shoff     = *(unsigned long *)(buf + 40);
        shentsize = *(unsigned short *)(buf + 58);
        shnum     = *(unsigned short *)(buf + 60);
        shstrndx  = *(unsigned short *)(buf + 62);
    }

    // Read section name string table header
    if (shnum == 0 || shentsize == 0) return 0; // stripped, no sections

    lseek(fd, shoff + (unsigned long)shstrndx * shentsize, SEEK_SET);
    unsigned char shstrtab_hdr[64];
    read(fd, shstrtab_hdr, sizeof(shstrtab_hdr));
    unsigned long shstrtab_off;
    if (buf[4] == 1)
        shstrtab_off = *(unsigned int *)(shstrtab_hdr + 16);
    else
        shstrtab_off = *(unsigned long *)(shstrtab_hdr + 24);

    // Scan section headers for .ARM.attributes
    for (unsigned short i = 0; i < shnum; i++) {
        lseek(fd, shoff + (unsigned long)i * shentsize, SEEK_SET);
        unsigned char shdr[64];
        read(fd, shdr, sizeof(shdr));

        unsigned int sh_name = *(unsigned int *)shdr;

        // Read section name
        char namebuf[32];
        lseek(fd, shstrtab_off + sh_name, SEEK_SET);
        read(fd, namebuf, sizeof(namebuf) - 1);
        namebuf[sizeof(namebuf) - 1] = '\0';

        if (strcmp(namebuf, ".ARM.attributes") != 0) continue;

        // Found it — get offset and size
        unsigned long attr_off, attr_size;
        if (buf[4] == 1) {
            attr_off  = *(unsigned int *)(shdr + 16);
            attr_size = *(unsigned int *)(shdr + 20);
        } else {
            attr_off  = *(unsigned long *)(shdr + 24);
            attr_size = *(unsigned long *)(shdr + 32);
        }

        // Read the section
        unsigned char *attr = malloc(attr_size);
        if (!attr) break;
        lseek(fd, attr_off, SEEK_SET);
        read(fd, attr, attr_size);

        // Parse ARM build attributes (simplified: scan for known tags)
        // Section format: 'A' version length "aeabi" subsections...
        // We scan for Tag_CPU_arch (6) and Tag_ABI_VFP_args (28) = 0x1c
        char *p = (char *)attr;
        char *end = p + attr_size;
        while (p + 2 < end) {
            int tag = (unsigned char)p[0];
            int len = 0;
            // ULEB128 length
            if (p[1] < 0x80) len = p[1];
            else if (p[1] < 0xc0) len = ((p[1] & 0x7f) << 7) | (p[2] & 0x7f);
            else break;

            if (p + 2 + len > end) break;

            switch (tag) {
            case 6: { // Tag_CPU_arch
                int arch = (unsigned char)p[2]; // uleb128, typically small
                char buf[8];
                snprintf(buf, sizeof(buf), "v%d", arch);
                print_field("cpu_arch", buf);
                break;
            }
            case 28: { // Tag_ABI_VFP_args
                int vfp = (unsigned char)p[2];
                print_field("float_abi", vfp == 1 ? "hard" : "soft");
                break;
            }
            }

            p += 2 + len;
        }

        free(attr);
        break;
    }

    return 0;
}
