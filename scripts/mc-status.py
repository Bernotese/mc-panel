#!/usr/bin/env python3
"""Minecraft Java Edition status ping (server list ping).

Unlike generic tools it sends an explicit server address in the handshake, which is what
mc-router routes on. Usage: mc-status.py HOST PORT SERVER_ADDRESS
Prints the status JSON and exits 0, or prints the error and exits 1.

With --login NAME it starts a login instead (protocol from a status ping first; Login Start
with name and UUID, Minecraft 1.20.2 and later) and prints the server's first answer:
"login: encryption_request" (online mode), "login: set_compression" or "login: login_success"
exit 0, "disconnect: <reason>" (e.g. "Connection throttled!") exits 1.
"""
import json
import socket
import struct
import sys
import uuid


def varint(n: int) -> bytes:
    n &= 0xFFFFFFFF
    out = bytearray()
    while True:
        b = n & 0x7F
        n >>= 7
        if n:
            out.append(b | 0x80)
        else:
            out.append(b)
            return bytes(out)


def packet(packet_id: int, payload: bytes) -> bytes:
    body = varint(packet_id) + payload
    return varint(len(body)) + body


def recv_exact(sock: socket.socket, n: int) -> bytes:
    buf = bytearray()
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise EOFError("connection closed by server")
        buf += chunk
    return bytes(buf)


def read_varint_sock(sock: socket.socket) -> int:
    value = shift = 0
    while True:
        b = recv_exact(sock, 1)[0]
        value |= (b & 0x7F) << shift
        if not b & 0x80:
            return value
        shift += 7
        if shift > 35:
            raise ValueError("varint too long")


def read_varint_buf(buf: bytes, pos: int) -> tuple[int, int]:
    value = shift = 0
    while True:
        b = buf[pos]
        pos += 1
        value |= (b & 0x7F) << shift
        if not b & 0x80:
            return value, pos
        shift += 7
        if shift > 35:
            raise ValueError("varint too long")


def status(host: str, port: int, server_address: str) -> dict:
    addr = server_address.encode("utf-8")
    handshake = varint(-1) + varint(len(addr)) + addr + struct.pack(">H", port) + varint(1)
    with socket.create_connection((host, port), timeout=10) as sock:
        sock.sendall(packet(0x00, handshake))
        sock.sendall(packet(0x00, b""))
        length = read_varint_sock(sock)
        if length <= 0 or length > 1 << 21:
            raise ValueError(f"implausible packet length {length}")
        data = recv_exact(sock, length)
    packet_id, pos = read_varint_buf(data, 0)
    if packet_id != 0x00:
        raise ValueError(f"unexpected packet id {packet_id}")
    strlen, pos = read_varint_buf(data, pos)
    return json.loads(data[pos : pos + strlen].decode("utf-8"))


LOGIN_ANSWERS = {0x01: "encryption_request", 0x02: "login_success", 0x03: "set_compression"}


def login(host: str, port: int, server_address: str, name: str) -> tuple[bool, str]:
    protocol = int(status(host, port, server_address)["version"]["protocol"])
    addr = server_address.encode("utf-8")
    handshake = varint(protocol) + varint(len(addr)) + addr + struct.pack(">H", port) + varint(2)
    raw = name.encode("utf-8")
    start = varint(len(raw)) + raw + uuid.uuid3(uuid.NAMESPACE_OID, name).bytes
    with socket.create_connection((host, port), timeout=10) as sock:
        sock.sendall(packet(0x00, handshake))
        sock.sendall(packet(0x00, start))
        length = read_varint_sock(sock)
        if length <= 0 or length > 1 << 21:
            raise ValueError(f"implausible packet length {length}")
        data = recv_exact(sock, length)
    packet_id, pos = read_varint_buf(data, 0)
    if packet_id == 0x00:
        strlen, pos = read_varint_buf(data, pos)
        return False, "disconnect: " + data[pos : pos + strlen].decode("utf-8", "replace")
    if packet_id not in LOGIN_ANSWERS:
        raise ValueError(f"unexpected login packet id {packet_id}")
    return True, "login: " + LOGIN_ANSWERS[packet_id]


def main() -> int:
    args = sys.argv[1:]
    if len(args) == 5 and args[3] == "--login":
        try:
            ok, answer = login(args[0], int(args[1]), args[2], args[4])
        except (OSError, EOFError, ValueError, KeyError, json.JSONDecodeError) as exc:
            print(f"mc-status: {exc}", file=sys.stderr)
            return 1
        print(answer)
        return 0 if ok else 1
    if len(args) != 3:
        print("usage: mc-status.py HOST PORT SERVER_ADDRESS [--login NAME]", file=sys.stderr)
        return 2
    try:
        result = status(args[0], int(args[1]), args[2])
    except (OSError, EOFError, ValueError, json.JSONDecodeError) as exc:
        print(f"mc-status: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
