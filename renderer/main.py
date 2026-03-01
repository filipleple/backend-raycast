import os
import socket
import math
import random
import threading
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
GREY  = (200, 200, 200)
RED   = (255, 0, 0)

# Object colors
WALL_COLOR = GREY
PANE_COLOR = RED

# FOV/raycasting settings
FOV_ANGLE    = 60
NUM_RAYS     = 120
PLAYER_SPEED = 10
TURN_SPEED   = 0.1
PLAYER_MARGIN = 8  # collision radius in pixels

# Protocol settings
HOST = "127.0.0.1"
PORT = 9000

SPRITE_PATH  = os.path.join(os.path.dirname(__file__), '..', 'hatman.gif')
NUM_MONSTERS = 3


@dataclass
class Monster:
    x: float
    y: float


class WorldState:
    """Shared across all connected players. Generated once by the first joiner."""
    def __init__(self):
        self.cols      = WIDTH  // GRID_SIZE
        self.rows      = HEIGHT // GRID_SIZE
        self.tile_size = min(WIDTH // self.cols, HEIGHT // self.rows)
        self.grid      = generate_map(self.cols, self.rows, fill=0.35, seed=None)

        empty_cells = [
            (c, r) for r in range(self.rows) for c in range(self.cols)
            if self.grid[r][c] == EMPTY
        ]
        random.shuffle(empty_cells)
        self.monsters = [
            Monster((c + 0.5) * self.tile_size, (r + 0.5) * self.tile_size)
            for c, r in empty_cells[:NUM_MONSTERS]
        ]


class PlayerState:
    """Per-connection state: position, angle, UI toggles."""
    def __init__(self, world):
        self.cam_angle   = 0.0
        self.show_map    = False
        self.prev_inputs = {}

        # spawn at empty cell closest to grid origin
        best, best_dist = None, float('inf')
        for row in range(world.rows):
            for col in range(world.cols):
                if world.grid[row][col] == EMPTY:
                    dist = math.hypot(col, row)
                    if dist < best_dist:
                        best_dist = dist
                        best = (col, row)
        col, row = best
        self.playerX = (col + 0.5) * world.tile_size
        self.playerY = (row + 0.5) * world.tile_size


class Renderer:
    def __init__(self, width=WIDTH, height=HEIGHT):
        self.width  = width
        self.height = height
        self.hatman = Image.open(SPRITE_PATH).convert("RGBA")

    def render(self, player, world, others=()):
        img     = np.zeros((self.height, self.width, 3), dtype=np.uint8)
        pil_img = Image.fromarray(img, mode="RGB")
        draw    = ImageDraw.Draw(pil_img)

        if player.show_map:
            self.draw_wall_map(draw, world)

        distances, sides = self.cast_fov_on_state(player, world)
        self.render_panes(draw, distances, FOV_ANGLE, world.tile_size)
        self.render_sprites(pil_img, player, world, distances, others)

        return pil_img

    def encode_png(self, pil_img):
        buf = io.BytesIO()
        pil_img.save(buf, format="PNG")
        return buf.getvalue()

    def write_png(self, player, world, path):
        with open(path, "wb") as f:
            f.write(self.encode_png(self.render(player, world)))

    def draw_wall_map(self, draw, world):
        for y in range(world.rows):
            for x in range(world.cols):
                if world.grid[y][x] == 1:
                    rect = (
                        x * world.tile_size,       y * world.tile_size,
                        (x + 1) * world.tile_size, (y + 1) * world.tile_size,
                    )
                    draw.rectangle(rect, fill=WALL_COLOR)

    def cast_fov_on_state(self, player, world):
        return cast_fov(
            world.grid, world.cols, world.rows, world.tile_size,
            player.playerX, player.playerY, player.cam_angle,
            FOV_ANGLE, NUM_RAYS,
        )

    def render_panes(self, draw, distances, fov_angle, tile_size):
        pane_width = WIDTH / NUM_RAYS
        fov        = radians(fov_angle)
        proj_plane = (WIDTH / 2) / tan(fov / 2)
        for i in range(NUM_RAYS):
            pane_x = int(i * pane_width)
            offset = (i / (NUM_RAYS - 1) - 0.5) * fov
            dist   = distances[i] * cos(offset)  # fish-eye correction
            if dist <= 0.0001 or dist == float("inf"):
                continue
            pane_height = min((tile_size / dist) * proj_plane, HEIGHT)
            y    = HEIGHT / 2 - pane_height / 2
            rect = (pane_x, y, pane_x + int(pane_width) + 1, y + pane_height)
            draw.rectangle(rect, outline=PANE_COLOR)

    def render_sprites(self, pil_img, player, world, distances, others=()):
        fov        = radians(FOV_ANGLE)
        proj_plane = (WIDTH / 2) / tan(fov / 2)

        # collect all sprite positions: monsters + other players
        positions = (
            [(m.x, m.y) for m in world.monsters] +
            [(p.playerX, p.playerY) for p in others]
        )

        # paint farthest first so near sprites overdraw far ones
        positions.sort(
            key=lambda s: math.hypot(s[0] - player.playerX, s[1] - player.playerY),
            reverse=True,
        )

        for sx, sy in positions:
            dx   = sx - player.playerX
            dy   = sy - player.playerY
            dist = math.hypot(dx, dy)
            if dist < 0.1:
                continue

            # angle to monster relative to camera, normalised to (-π, π)
            sprite_angle = math.atan2(dy, dx) - player.cam_angle
            sprite_angle = (sprite_angle + math.pi) % (2 * math.pi) - math.pi

            if abs(sprite_angle) > fov / 2 + 0.2:
                continue

            sprite_h = max(1, min(int((world.tile_size / dist) * proj_plane), HEIGHT))
            sprite_w = sprite_h  # square billboard
            screen_x = int((sprite_angle / fov + 0.5) * WIDTH)
            draw_x   = screen_x - sprite_w // 2
            draw_y   = HEIGHT // 2 - sprite_h // 2

            scaled = self.hatman.resize((sprite_w, sprite_h), Image.NEAREST)

            for col in range(sprite_w):
                screen_col = draw_x + col
                if not (0 <= screen_col < WIDTH):
                    continue
                ray_i      = max(0, min(int(screen_col / WIDTH * NUM_RAYS), NUM_RAYS - 1))
                offset     = (ray_i / (NUM_RAYS - 1) - 0.5) * fov
                perp_wall  = distances[ray_i] * cos(offset)
                if dist >= perp_wall:
                    continue
                strip = scaled.crop((col, 0, col + 1, sprite_h))
                pil_img.paste(strip, (screen_col, draw_y), strip)


def update(player, world, inputs):
    if inputs.get("ArrowLeft"):
        player.cam_angle -= TURN_SPEED
    if inputs.get("ArrowRight"):
        player.cam_angle += TURN_SPEED

    dirX   = math.cos(player.cam_angle)
    dirY   = math.sin(player.cam_angle)
    rightX = -dirY
    rightY =  dirX

    moveX = moveY = 0.0

    if inputs.get("m") and not player.prev_inputs.get("m"):
        player.show_map = not player.show_map

    if inputs.get("ArrowUp"):
        moveX += dirX * PLAYER_SPEED
        moveY += dirY * PLAYER_SPEED
    if inputs.get("ArrowDown"):
        moveX -= dirX * PLAYER_SPEED
        moveY -= dirY * PLAYER_SPEED
    if inputs.get("a"):
        moveX -= rightX * PLAYER_SPEED
        moveY -= rightY * PLAYER_SPEED
    if inputs.get("d"):
        moveX += rightX * PLAYER_SPEED
        moveY += rightY * PLAYER_SPEED

    mag = math.hypot(moveX, moveY)
    if mag > 0:
        moveX = moveX / mag * PLAYER_SPEED
        moveY = moveY / mag * PLAYER_SPEED

    newX = player.playerX + moveX
    newY = player.playerY + moveY

    ts = world.tile_size
    if moveX != 0:
        cx = int((newX + math.copysign(PLAYER_MARGIN, moveX)) / ts)
        cy = int(player.playerY / ts)
        if not (0 <= cx < world.cols and 0 <= cy < world.rows) or world.grid[cy][cx] == WALL:
            newX = player.playerX

    if moveY != 0:
        cx = int(newX / ts)
        cy = int((newY + math.copysign(PLAYER_MARGIN, moveY)) / ts)
        if not (0 <= cx < world.cols and 0 <= cy < world.rows) or world.grid[cy][cx] == WALL:
            newY = player.playerY

    player.playerX    = newX
    player.playerY    = newY
    player.prev_inputs = inputs


# --- shared server state ---
renderer = Renderer()
world    = None
players  = []
lock     = threading.Lock()


def handle_client(conn):
    global world
    with lock:
        if world is None:
            world = WorldState()
        player = PlayerState(world)
        players.append(player)
    try:
        with conn:
            while True:
                try:
                    inputs = recv_json(conn)
                except ConnectionError:
                    break
                update(player, world, inputs)
                with lock:
                    others = [p for p in players if p is not player]
                pil_img = renderer.render(player, world, others)
                png     = renderer.encode_png(pil_img)
                send_frame(conn, png)
    finally:
        with lock:
            players.remove(player)


#
# Main entry point
#
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((HOST, PORT))
    s.listen()
    while True:
        conn, addr = s.accept()
        threading.Thread(target=handle_client, args=(conn,), daemon=True).start()
