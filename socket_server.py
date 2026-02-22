import socket
import struct
import json

HOST = "127.0.0.1"
PORT = 9000

def recv_exact(sock, n):
    data = bytearray()
    while len(data) < n:
        chunk = sock.recv(n - len(data))
        if not chunk:
            # EOF before full frame
            raise ConnectionError("EOF mid-frame")
        data.extend(chunk)
    return bytes(data)


def recv_frame(sock):
    # read 4-byte length
    header = recv_exact(sock, 4)
    length = struct.unpack("!I", header)[0]  # big-endian unsigned int

    # read payload
    payload = recv_exact(sock, length)

    # decode JSON
    return json.loads(payload.decode("utf-8"))


with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.bind((HOST, PORT))
    s.listen(1)

    conn, addr = s.accept()
    with conn:
        msg = recv_frame(conn)
        print(msg)
