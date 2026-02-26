import socket
from protocol import recv_json, send_frame
from math import cos, tan, radians
from mapgen import generate_map
from fov import cast_fov
from PIL import Image, ImageDraw
import numpy as np
import io

# Window settings
WIDTH, HEIGHT = 800, 800
WALL = 1
EMPTY = 0

# Colors
WHITE = (255, 255, 255)
BLACK = (0, 0, 0)
GREY = (200, 200, 200)
RED = (255, 0, 0)

# Object colors
WALL_COLOR = GREY
PANE_COLOR = RED

# FOV/raycasting settings
FOV_ANGLE = 60
NUM_RAYS = 120

# Protocol settings
HOST = "127.0.0.1"
PORT = 9000

class Renderer:
    def __init__(self, width=WIDTH, height=HEIGHT):
        self.width = width
        self.height = height

    def render(self, state):
        img = np.zeros((self.height, self.width, 3), dtype=np.uint8)

        # convert to Pillow canvas
        pil_img = Image.fromarray(img, mode="RGB")
        draw = ImageDraw.Draw(pil_img)

        self.draw_wall_map(draw, state)
        distances, sides = self.cast_fov_on_state(state)
        self.render_panes(draw, distances, FOV_ANGLE, state.tile_size)

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

    def draw_wall_map(self, draw, state):
        rows = len(state.grid)
        cols = len(state.grid[0]) if rows else 0

        for y in range(rows):
            for x in range(cols):
                if state.grid[y][x] == 1:
                    rect = (x * state.tile_size, y * state.tile_size, (x + 1) * state.tile_size, (y + 1) * state.tile_size)
                    draw.rectangle(rect, fill=WALL_COLOR)

    def cast_fov_on_state(self, state):
        return cast_fov(state.grid, state.cols, state.rows, state.tile_size,
                 state.originX, state.originY, state.cam_angle,
                 FOV_ANGLE, NUM_RAYS)

    def render_panes(self, draw, distances, fov_angle, tile_size):
        pane_width = WIDTH / NUM_RAYS
        fov = radians(fov_angle)
        proj_plane = (WIDTH / 2) / tan(fov / 2)
        for i in range(NUM_RAYS):
            pane_x = int(i * pane_width)

            offset = (i / (NUM_RAYS - 1) - 0.5) * fov
            dist = distances[i]
            dist = dist * cos(offset)  # fish-eye correction

            if dist <= 0.0001 or dist == float("inf"):
                continue

            pane_height = (tile_size / dist) * proj_plane
            pane_height = min(pane_height, HEIGHT)  # clamp so it does not explode

            y = HEIGHT / 2 - pane_height / 2
            rect = (pane_x, y, pane_x + int(pane_width) + 1, y + pane_height)
            draw.rectangle(rect, outline=PANE_COLOR)

class GameState:
    def __init__(self):
        self.cols, self.rows = 8, 8
        self.tile_size = min(WIDTH // self.cols, HEIGHT // self.rows)
        self.grid = generate_map(self.cols, self.rows, fill=0.35, seed=None)
        self.cam_angle = 0.0
        self.originX, self.originY = 0,0

def update(state, inputs):
    # inputs ignored for now, using pygame get
    
    # for event in pygame.event.get():
    #     if event.type == pygame.QUIT:
    #         return False
    #     if event.type == pygame.MOUSEWHEEL:
    #         state.cam_angle += event.y*0.1

    #     if event.type == pygame.KEYDOWN:
    #         if event.key == pygame.K_SPACE:
    #             renderer.write_png(state, "test.png")
    #             print("Space!")

    # # Get the mouse position
    # state.originX, state.originY = pygame.mouse.get_pos()
    # 
    state.cam_angle = state.cam_angle % 360
    
renderer = Renderer()
state = GameState()

def handle_client(conn):
    state = GameState()
    renderer = Renderer()

    with conn:
        while True:
            try:
                inputs = recv_json(conn)
                # print("received inputs")
                # print(inputs)
            except ConnectionError:
                break
            update(state, inputs)
            pil_img = renderer.render(state)
            png = renderer.encode_png(pil_img)
            send_frame(conn, png)

#
# Main entry point
# 
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.bind((HOST, PORT))
    s.listen(1)

    while True:
        conn, addr = s.accept()
        handle_client(conn)
