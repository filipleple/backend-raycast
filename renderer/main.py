import os
import socket
import math
import random
from dataclasses import dataclass
from protocol import recv_json, send_frame
from math import cos, tan, radians
from mapgen import generate_map
from fov import cast_fov
from PIL import Image, ImageDraw
import numpy as np
import io

# Window settings
WIDTH, HEIGHT = 640, 480
WALL = 1
EMPTY = 0
GRID_SIZE = 50

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
PLAYER_SPEED = 10
TURN_SPEED = 0.1
PLAYER_MARGIN = 8  # collision radius in pixels

# Protocol settings
HOST = "127.0.0.1"
PORT = 9000

SPRITE_PATH = os.path.join(os.path.dirname(__file__), '..', 'hatman.gif')
NUM_MONSTERS = 3

@dataclass
class Monster:
    x: float
    y: float

class Renderer:
    def __init__(self, width=WIDTH, height=HEIGHT):
        self.width = width
        self.height = height
        self.hatman = Image.open(SPRITE_PATH).convert("RGBA")

    def render(self, state):
        img = np.zeros((self.height, self.width, 3), dtype=np.uint8)

        # convert to Pillow canvas
        pil_img = Image.fromarray(img, mode="RGB")
        draw = ImageDraw.Draw(pil_img)

        if (state.show_map):
            self.draw_wall_map(draw, state)

        distances, sides = self.cast_fov_on_state(state)
        self.render_panes(draw, distances, FOV_ANGLE, state.tile_size)
        self.render_sprites(pil_img, state, distances)

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
                 state.playerX, state.playerY, state.cam_angle,
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

    def render_sprites(self, pil_img, state, distances):
        fov = radians(FOV_ANGLE)
        proj_plane = (WIDTH / 2) / tan(fov / 2)

        # paint farthest first so near sprites overdraw far ones
        sorted_monsters = sorted(
            state.monsters,
            key=lambda m: math.hypot(m.x - state.playerX, m.y - state.playerY),
            reverse=True,
        )

        for monster in sorted_monsters:
            dx = monster.x - state.playerX
            dy = monster.y - state.playerY
            dist = math.hypot(dx, dy)
            if dist < 0.1:
                continue

            # angle to monster relative to camera direction, normalised to (-π, π)
            sprite_angle = math.atan2(dy, dx) - state.cam_angle
            sprite_angle = (sprite_angle + math.pi) % (2 * math.pi) - math.pi

            # cull anything clearly outside the FOV
            if abs(sprite_angle) > fov / 2 + 0.2:
                continue

            sprite_h = int((state.tile_size / dist) * proj_plane)
            sprite_h = max(1, min(sprite_h, HEIGHT))
            sprite_w = sprite_h  # square billboard

            screen_x = int((sprite_angle / fov + 0.5) * WIDTH)
            draw_x = screen_x - sprite_w // 2
            draw_y = HEIGHT // 2 - sprite_h // 2

            scaled = self.hatman.resize((sprite_w, sprite_h), Image.NEAREST)

            for col in range(sprite_w):
                screen_col = draw_x + col
                if not (0 <= screen_col < WIDTH):
                    continue

                # ray index for this screen column
                ray_i = int(screen_col / WIDTH * NUM_RAYS)
                ray_i = max(0, min(NUM_RAYS - 1, ray_i))

                # fish-eye corrected perpendicular wall distance at this column
                offset = (ray_i / (NUM_RAYS - 1) - 0.5) * fov
                perp_wall = distances[ray_i] * cos(offset)

                if dist >= perp_wall:
                    continue  # wall is closer, skip

                strip = scaled.crop((col, 0, col + 1, sprite_h))
                pil_img.paste(strip, (screen_col, draw_y), strip)

class GameState:
    def __init__(self):
        self.show_map = False
        self.prev_inputs = {}
        self.cols, self.rows = WIDTH//GRID_SIZE, HEIGHT//GRID_SIZE
        self.tile_size = min(WIDTH // self.cols, HEIGHT // self.rows)
        self.grid = generate_map(self.cols, self.rows, fill=0.35, seed=None)
        self.cam_angle = 0.0
        # find empty cell closest to (0,0) to avoid spawning inside a wall
        best, best_dist = None, float('inf')
        for row in range(self.rows):
            for col in range(self.cols):
                if self.grid[row][col] == EMPTY:
                    dist = math.hypot(col, row)
                    if dist < best_dist:
                        best_dist = dist
                        best = (col, row)
        col, row = best
        self.playerX = (col + 0.5) * self.tile_size
        self.playerY = (row + 0.5) * self.tile_size

        empty_cells = [
            (c, r) for r in range(self.rows) for c in range(self.cols)
            if self.grid[r][c] == EMPTY and (c, r) != (col, row)
        ]
        random.shuffle(empty_cells)
        self.monsters = [
            Monster((c + 0.5) * self.tile_size, (r + 0.5) * self.tile_size)
            for c, r in empty_cells[:NUM_MONSTERS]
        ]


def update(state, inputs):
    # --- turning ---
    if inputs.get("ArrowLeft"):
        state.cam_angle -= TURN_SPEED
    if inputs.get("ArrowRight"):
        state.cam_angle += TURN_SPEED

    # --- direction vectors from angle ---
    dirX = math.cos(state.cam_angle)
    dirY = math.sin(state.cam_angle)

    # perpendicular (strafe)
    rightX = -dirY
    rightY =  dirX

    moveX = 0.0
    moveY = 0.0

    if inputs.get("m") and not state.prev_inputs.get("m"):
        state.show_map = not state.show_map

    # --- forward/back ---
    if inputs.get("ArrowUp"):
        moveX += dirX * PLAYER_SPEED
        moveY += dirY * PLAYER_SPEED
    if inputs.get("ArrowDown"):
        moveX -= dirX * PLAYER_SPEED
        moveY -= dirY * PLAYER_SPEED

    # --- strafe (example: A/D) ---
    if inputs.get("a"):
        moveX -= rightX * PLAYER_SPEED
        moveY -= rightY * PLAYER_SPEED
    if inputs.get("d"):
        moveX += rightX * PLAYER_SPEED
        moveY += rightY * PLAYER_SPEED

    # optional: normalize diagonal movement so it stays at PLAYER_SPEED
    mag = math.hypot(moveX, moveY)
    if mag > 0:
        moveX = moveX / mag * PLAYER_SPEED
        moveY = moveY / mag * PLAYER_SPEED

    newX = state.playerX + moveX
    newY = state.playerY + moveY

    # axis-split collision: check X and Y independently for wall sliding
    ts = state.tile_size
    if moveX != 0:
        cx = int((newX + math.copysign(PLAYER_MARGIN, moveX)) / ts)
        cy = int(state.playerY / ts)
        if not (0 <= cx < state.cols and 0 <= cy < state.rows) or state.grid[cy][cx] == WALL:
            newX = state.playerX

    if moveY != 0:
        cx = int(newX / ts)
        cy = int((newY + math.copysign(PLAYER_MARGIN, moveY)) / ts)
        if not (0 <= cx < state.cols and 0 <= cy < state.rows) or state.grid[cy][cx] == WALL:
            newY = state.playerY

    state.playerX = newX
    state.playerY = newY
    state.prev_inputs = inputs
    
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
