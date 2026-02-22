import socket
import struct
import json

msg = {"left": True}
data = json.dumps(msg).encode("utf-8")


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


frame = struct.pack("!I", len(data)) + data

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.connect(("127.0.0.1", 9000))
    s.sendall(frame)
    png = recv_binary(s)
    with open("test.png", "wb") as f:
        f.write(png)
