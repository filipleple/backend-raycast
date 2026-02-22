import socket
import json
from protocol import send_frame, recv_binary

msg = {"right": True}
data = json.dumps(msg).encode("utf-8")

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.connect(("127.0.0.1", 9000))

    send_frame(s, data)
    png = recv_binary(s)
    with open("test.png", "wb") as f:
        f.write(png)
