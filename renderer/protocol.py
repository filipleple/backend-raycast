import json
import struct


def recv_exact(sock, n):
    data = bytearray()
    while len(data) < n:
        chunk = sock.recv(n - len(data))
        if not chunk:
            # EOF before full frame
            raise ConnectionError("EOF mid-frame")
        data.extend(chunk)
    return bytes(data)

def recv_frame_bytes(sock):
    header = recv_exact(sock, 4)
    length = struct.unpack("!I", header)[0]
    return recv_exact(sock, length)

def recv_json(sock):
    payload = recv_frame_bytes(sock)
    return json.loads(payload.decode("utf-8"))

def recv_binary(sock):
    return recv_frame_bytes(sock)

def send_frame(sock, payload_bytes):
    header = struct.pack("!I", len(payload_bytes))
    sock.sendall(header + payload_bytes)
