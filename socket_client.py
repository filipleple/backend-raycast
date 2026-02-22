import socket
import struct
import json

msg = {"left": True}
data = json.dumps(msg).encode("utf-8")

frame = struct.pack("!I", len(data)) + data

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.connect(("127.0.0.1", 9000))
    s.sendall(frame)
