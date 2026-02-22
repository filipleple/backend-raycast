import socket
from protocol import recv_json, send_frame
import numpy as np
import io
from PIL import Image, ImageDraw

# Coordinate system:
# (0,0) = top-left
# x increases right
# y increases downward

# defaults
FRAME_HEIGHT, FRAME_WIDTH = 480, 640
PLAYER_SIDE = 80
PLAYER_COLOR = (0, 200, 0)
PLAYER_SPEED = 5

HOST = "127.0.0.1"
PORT = 9000

#
# GAME STATE / RENDERING
#
class Renderer:
    def __init__(self, width, height):
        self.width = width
        self.height = height

    def render(self, state):
        img = np.zeros((self.height, self.width, 3), dtype=np.uint8)

        # convert to Pillow canvas
        pil_img = Image.fromarray(img, mode="RGB")
        draw = ImageDraw.Draw(pil_img)

        draw.rectangle(state.player_bounds(), fill=state.player_color)

        return pil_img

    def encode_png(self, pil_img):
        # store in PNG format
        buf = io.BytesIO()
        pil_img.save(buf, format="PNG")
        png_bytes = buf.getvalue()
        return png_bytes

    def write_png(self, state, path):
        png_bytes = self.encode_png(self.render(state))
        # write to disk
        with open(path, "wb") as f:
            f.write(png_bytes)
        
class GameState:
    def __init__(self):
        self.player_x = 5
        self.player_y = 5       
        self.player_side = PLAYER_SIDE
        self.player_color = PLAYER_COLOR

    def player_bounds(self):
        return (
            self.player_x,
            self.player_y,
            self.player_x + PLAYER_SIDE,
            self.player_y + PLAYER_SIDE,
        )

def update(state, inputs):
    if (inputs.get("left")):
        state.player_x -= PLAYER_SPEED
    if (inputs.get("right")):
        state.player_x += PLAYER_SPEED
    if (inputs.get("up")):
        state.player_y -= PLAYER_SPEED
    if (inputs.get("down")):
        state.player_y += PLAYER_SPEED

    state.player_x = max(0, min(state.player_x, FRAME_WIDTH - PLAYER_SIDE))
    state.player_y = max(0, min(state.player_y, FRAME_HEIGHT - state.player_side))

def handle_client(conn):
    state = GameState()
    renderer = Renderer(FRAME_WIDTH, FRAME_HEIGHT)

    with conn:
        while True:
            try:
                inputs = recv_json(conn)
            except ConnectionError:
                break
            update(state, inputs)
            pil_img = renderer.render(state)
            png = renderer.encode_png(pil_img)
            send_frame(conn, png)

#
# Main entry point
# 
state = GameState()
renderer = Renderer(FRAME_WIDTH, FRAME_HEIGHT)

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.bind((HOST, PORT))
    s.listen(1)

    conn, addr = s.accept()
    handle_client(conn)
