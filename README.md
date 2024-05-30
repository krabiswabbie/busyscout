# BusyScout

BusyScout is a utility designed for file upload over telnet, specifically targeting devices such as budget IP cameras that are built with BusyBox and typically lack conventional file transfer capabilities.

## Table of Contents

- [Introduction](#introduction)
- [Usage](#usage)
- [Rationale](#rationale)
- [Method of Transfer](#method-of-transfer)
- [Advantages](#advantages)
- [Disadvantages](#disadvantages)
- [Security Note](#security-note)
- [License](#license)

## Introduction

This utility aims to enable file uploads to devices where traditional methods are not available, utilizing only telnet as the medium. BusyScout exploits the basic system functionalities to simulate file transfer capabilities in environments where only telnet access is possible.

## Usage

Download the compiled version for your platform from the [Releases](https://github.com/<your-username>/busyscout/releases) section OR build the utility from the source code provided.

```bash
./busyscout ipwiz.zip root:root@192.168.10.18:/tmp
```

## Rationale
Budget IP cameras, particularly from Hikvision, Dahua etc, often use [BusyBox](https://busybox.net/) and may allow telnet access but not SSH. Other file transfer possibilities like `mount`, `tftp`, or `nc` might be occasionally available, but some cameras restrict all conventional file transfer methods. BusyScout fills this gap by allowing file transfers strictly through telnet.

## Method of Transfer
Telnet protocol does not inherently support file transfers. However, an alternative approach involves using the telnet console to invoke the `printf` function to transmit bytes, which are then redirected into a file using standard Linux commands. Example commands include:

```bash
printf "\xDE\xAD\xBE\xEF\x...\xF0" > /tmp/bs.0001.part
printf "\xCA\xFE\x33\xE1\x...\xD3" > /tmp/bs.0002.part
...
cat /tmp/bs.*.part > targetfile 
```

For efficiency, file transmission is executed in parallel across multiple telnet sessions, and the data is subsequently merged into a single file.

This method was initially described [here](https://unix.stackexchange.com/a/417895)

## Advantages
- Utilizes only widely available system functions, requiring no external commands or utilities.
- Capable of transferring files in environments where other methods fail.

## Disadvantages
- Low transfer speed, something about 3-5 KB/s is really nice.
- No data integrity verification such as CRC etc.

## Security Note
The telnet protocol was designed in an era before security was a primary concern. While it may be the only method of interaction in some scenarios, using it comes with inherent risks. Use at your own risk.

## License
[MIT License](LICENSE)
