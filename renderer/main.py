import os
import socket
import math
import random
import threading
from dataclasses import dataclass
from protocol import recv_json, send_frame, recv_binary
from math import cos, tan, radians
from mapgen import load_map, find_spawn, make_csv_doors, Door, PortalDoor
from fov import cast_fov
from PIL import Image, ImageDraw
import numpy as np
import io
import logging

# Window settings
WIDTH, HEIGHT = 320, 240

# Colors
GREY    = (200, 200, 200)
RED     = (255, 0, 0)
BROWN   = (100,  60,  20)   # connectivity door on minimap
MAGENTA = (220,   0, 220)   # reserved for portal doors

# Object colors
WALL_COLOR = GREY
PANE_COLOR = RED

# FOV/raycasting settings
FOV_ANGLE     = 60
NUM_RAYS      = 120
PLAYER_SPEED  = 10
TURN_SPEED    = 0.1
PLAYER_MARGIN = 8  # collision radius in pixels

# Protocol settings
HOST = "0.0.0.0"
PORT = 9000

# Asset paths
ASSETS            = os.path.join(os.path.dirname(__file__), '..')
SPRITE_PATH       = os.path.join(ASSETS, 'hatman.gif')
FRAMES_DIR        = os.path.join(ASSETS, 'frames')
TEXTURES_DIR      = os.path.join(ASSETS, 'textures')

# Entity counts per map
NUM_MONSTERS = 0
NUM_FRAMES   = 3

# ---------------------------------------------------------------------------
# Entities
# ---------------------------------------------------------------------------

@dataclass
class Monster:
    x: float
    y: float


# ---------------------------------------------------------------------------
# Asset loaders
# ---------------------------------------------------------------------------

def load_frame_images(frames_dir):
    images = []
    if os.path.isdir(frames_dir):
        for fname in sorted(os.listdir(frames_dir)):
            if fname.lower().endswith('.gif'):
                img = Image.open(os.path.join(frames_dir, fname)).convert("RGBA")
                images.append(img)
    return images



# ---------------------------------------------------------------------------
# Map
# ---------------------------------------------------------------------------

class Map:
    """A self-contained room.

    textures:          {texture_name: PIL Image} loaded from definitions.
    door_cells:        same-map connectivity doors.
    portal_door_cells: cross-map portal doors (one per map).
    """
    def __init__(self, cols, rows, tile_size, grid,
                 textures, monsters, frame_cells,
                 door_cells, portal_door_cells):
        self.cols              = cols
        self.rows              = rows
        self.tile_size         = tile_size
        self.grid              = grid
        self.textures          = textures
        self.monsters          = monsters
        self.frame_cells       = frame_cells
        self.door_cells        = door_cells
        self.portal_door_cells = portal_door_cells


def build_map():
    grid, door_positions, cols, rows = load_map()
    tile_size = min(WIDTH // cols, HEIGHT // rows)

    # load textures by name from each symbol's texture_name field
    textures = {}
    seen = set()
    for r in range(rows):
        for c in range(cols):
            sym = grid[r][c]
            if not sym.texture_name or sym.texture_name in seen:
                continue
            seen.add(sym.texture_name)
            subdir = 'doors' if sym.door else 'walls'
            path   = os.path.join(TEXTURES_DIR, subdir, sym.texture_name + '.gif')
            if os.path.isfile(path):
                textures[sym.texture_name] = Image.open(path).convert("RGB")

    empty_cells = [(c, r) for r in range(rows) for c in range(cols)
                   if grid[r][c].floor]
    random.shuffle(empty_cells)

    monsters = [Monster((c + 0.5) * tile_size, (r + 0.5) * tile_size)
                for c, r in empty_cells[:NUM_MONSTERS]]

    frame_images = load_frame_images(FRAMES_DIR)
    frame_cells  = {}
    if frame_images:
        wall_cells = [(c, r) for r in range(rows) for c in range(cols)
                      if not grid[r][c].floor]
        random.shuffle(wall_cells)
        for c, r in wall_cells[:NUM_FRAMES]:
            frame_cells[(c, r)] = random.choice(frame_images).convert("RGB")

    door_cells = make_csv_doors(door_positions, grid, cols, rows, tile_size)

    return Map(cols, rows, tile_size, grid,
               textures, monsters, frame_cells,
               door_cells, {})


# ---------------------------------------------------------------------------
# World
# ---------------------------------------------------------------------------

class WorldState:
    def __init__(self):
        self.maps = [build_map()]


# ---------------------------------------------------------------------------
# Player
# ---------------------------------------------------------------------------

class PlayerState:
    def __init__(self, world, player_id=0, avatar=None):
        self.world       = world
        self.current_map = world.maps[0]
        self.cam_angle   = 0.0
        self.show_map    = False
        self.prev_inputs = {}
        self.player_id   = player_id
        self.avatar      = avatar  # PIL Image or None; falls back to hatman
        self.playerX, self.playerY = find_spawn(self.current_map)


# ---------------------------------------------------------------------------
# Renderer
# ---------------------------------------------------------------------------

class Renderer:
    def __init__(self, width=WIDTH, height=HEIGHT):
        self.width  = width
        self.height = height
        self.hatman = Image.open(SPRITE_PATH).convert("RGBA")

    def render(self, player, others=()):
        m       = player.current_map
        img     = np.zeros((self.height, self.width, 3), dtype=np.uint8)
        pil_img = Image.fromarray(img, mode="RGB")
        draw    = ImageDraw.Draw(pil_img)

        if player.show_map:
            self.draw_wall_map(draw, m)

        distances, sides, uvs, cells = self.cast_fov(player, m)
        self.render_panes(draw, pil_img, distances, uvs, cells, m)
        self.render_sprites(pil_img, player, m, distances, others)

        return pil_img

    def encode_jpeg(self, pil_img, quality=60):
        buf = io.BytesIO()
        pil_img.convert("RGB").save(buf, format="JPEG", quality=quality)
        return buf.getvalue()

    def draw_wall_map(self, draw, m):
        for y in range(m.rows):
            for x in range(m.cols):
                if m.grid[y][x].floor:
                    continue
                if (x, y) in m.door_cells:
                    fill = BROWN
                else:
                    fill = WALL_COLOR
                draw.rectangle(
                    (x * m.tile_size, y * m.tile_size,
                     (x + 1) * m.tile_size, (y + 1) * m.tile_size),
                    fill=fill,
                )

    def cast_fov(self, player, m):
        return cast_fov(m.grid, m.cols, m.rows, m.tile_size,
                        player.playerX, player.playerY, player.cam_angle,
                        FOV_ANGLE, NUM_RAYS)

    def render_panes(self, draw, pil_img, distances, uvs, cells, m):
        pane_width = WIDTH / NUM_RAYS
        fov        = radians(FOV_ANGLE)
        proj_plane = (WIDTH / 2) / tan(fov / 2)

        for i in range(NUM_RAYS):
            pane_x = int(i * pane_width)
            offset = (i / (NUM_RAYS - 1) - 0.5) * fov
            dist   = distances[i] * cos(offset)
            if dist <= 0.0001 or dist == float("inf"):
                continue

            pane_height = min((m.tile_size / dist) * proj_plane, HEIGHT)
            y  = int(HEIGHT / 2 - pane_height / 2)
            pw = int(pane_width) + 1
            ph = int(pane_height)
            cx, cy = cells[i]

            tex = m.frame_cells.get((cx, cy))
            if tex is None:
                tex = m.textures.get(m.grid[cy][cx].texture_name)

            if tex is not None:
                tex_x = int(uvs[i] * tex.width) % tex.width
                strip = tex.crop((tex_x, 0, tex_x + 1, tex.height))
                strip = strip.resize((pw, ph), Image.NEAREST)
                pil_img.paste(strip.convert("RGB"), (pane_x, y))
            else:
                draw.rectangle((pane_x, y, pane_x + pw, y + ph), outline=PANE_COLOR)

    def render_sprites(self, pil_img, player, m, distances, others=()):
        fov        = radians(FOV_ANGLE)
        proj_plane = (WIDTH / 2) / tan(fov / 2)

        sprites = (
            [(mon.x, mon.y, self.hatman) for mon in m.monsters] +
            [(p.playerX, p.playerY, p.avatar if p.avatar is not None else self.hatman)
             for p in others if p.current_map is m]
        )
        sprites.sort(
            key=lambda s: math.hypot(s[0] - player.playerX, s[1] - player.playerY),
            reverse=True,
        )

        for sx, sy, img in sprites:
            dx   = sx - player.playerX
            dy   = sy - player.playerY
            dist = math.hypot(dx, dy)
            if dist < 0.1:
                continue

            sprite_angle = math.atan2(dy, dx) - player.cam_angle
            sprite_angle = (sprite_angle + math.pi) % (2 * math.pi) - math.pi
            if abs(sprite_angle) > fov / 2 + 0.2:
                continue

            sprite_h = max(1, min(int((m.tile_size / dist) * proj_plane), HEIGHT))
            sprite_w = sprite_h
            screen_x = int((sprite_angle / fov + 0.5) * WIDTH)
            draw_x   = screen_x - sprite_w // 2
            draw_y   = HEIGHT // 2 - sprite_h // 2

            scaled = img.resize((sprite_w, sprite_h), Image.NEAREST)

            for col in range(sprite_w):
                screen_col = draw_x + col
                if not (0 <= screen_col < WIDTH):
                    continue
                ray_i     = max(0, min(int(screen_col / WIDTH * NUM_RAYS), NUM_RAYS - 1))
                offset    = (ray_i / (NUM_RAYS - 1) - 0.5) * fov
                perp_wall = distances[ray_i] * cos(offset)
                if dist >= perp_wall:
                    continue
                strip = scaled.crop((col, 0, col + 1, sprite_h))
                pil_img.paste(strip, (screen_col, draw_y), strip)


# ---------------------------------------------------------------------------
# Game logic
# ---------------------------------------------------------------------------

def update(player, inputs):
    m = player.current_map

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

    # --- door / portal interaction ---
    if inputs.get(" ") and not player.prev_inputs.get(" "):
        look_x   = player.playerX + dirX * m.tile_size * 0.7
        look_y   = player.playerY + dirY * m.tile_size * 0.7
        look_col = int(look_x / m.tile_size)
        look_row = int(look_y / m.tile_size)

        # sample along look direction at multiple depths — robust to approach
        # angle and exact distance from the door
        conn_door = None
        for t in (0.3, 0.5, 0.7, 1.0, 1.3):
            lc = int((player.playerX + dirX * m.tile_size * t) / m.tile_size)
            lr = int((player.playerY + dirY * m.tile_size * t) / m.tile_size)
            d  = m.door_cells.get((lc, lr))
            if d:
                conn_door = d
                break

        if conn_door:
            dist_a = math.hypot(player.playerX - conn_door.exit_a[0],
                                player.playerY - conn_door.exit_a[1])
            dist_b = math.hypot(player.playerX - conn_door.exit_b[0],
                                player.playerY - conn_door.exit_b[1])
            if dist_a < dist_b:
                player.playerX, player.playerY = conn_door.exit_b
            else:
                player.playerX, player.playerY = conn_door.exit_a

        portal = m.portal_door_cells.get((look_col, look_row))
        if portal:
            if portal.target_map is None:
                new_map             = build_map()
                portal.target_map   = new_map
                portal.target_pos   = find_spawn(new_map)
                player.world.maps.append(new_map)
            player.current_map             = portal.target_map
            player.playerX, player.playerY = portal.target_pos

    if inputs.get("ArrowUp"):
        moveX += dirX * PLAYER_SPEED;  moveY += dirY * PLAYER_SPEED
    if inputs.get("ArrowDown"):
        moveX -= dirX * PLAYER_SPEED;  moveY -= dirY * PLAYER_SPEED
    if inputs.get("a"):
        moveX -= rightX * PLAYER_SPEED; moveY -= rightY * PLAYER_SPEED
    if inputs.get("d"):
        moveX += rightX * PLAYER_SPEED; moveY += rightY * PLAYER_SPEED

    mag = math.hypot(moveX, moveY)
    if mag > 0:
        moveX = moveX / mag * PLAYER_SPEED
        moveY = moveY / mag * PLAYER_SPEED

    # collision uses current_map, which may have just changed via portal
    m    = player.current_map
    newX = player.playerX + moveX
    newY = player.playerY + moveY
    ts   = m.tile_size

    if moveX != 0:
        cx = int((newX + math.copysign(PLAYER_MARGIN, moveX)) / ts)
        cy = int(player.playerY / ts)
        if not (0 <= cx < m.cols and 0 <= cy < m.rows) or not m.grid[cy][cx].floor:
            newX = player.playerX

    if moveY != 0:
        cx = int(newX / ts)
        cy = int((newY + math.copysign(PLAYER_MARGIN, moveY)) / ts)
        if not (0 <= cx < m.cols and 0 <= cy < m.rows) or not m.grid[cy][cx].floor:
            newY = player.playerY

    player.playerX     = newX
    player.playerY     = newY
    player.prev_inputs = inputs


# ---------------------------------------------------------------------------
# Server
# ---------------------------------------------------------------------------

renderer = Renderer()
world    = None
players  = []
lock     = threading.Lock()


def handle_client(conn):
    global world

    # --- handshake ---
    try:
        handshake    = recv_json(conn)
        avatar_bytes = recv_binary(conn)
    except ConnectionError:
        conn.close()
        return

    player_id = handshake.get("player_id", 0)
    avatar    = None
    if avatar_bytes:
        try:
            avatar = Image.open(io.BytesIO(avatar_bytes)).convert("RGBA")
        except Exception:
            pass  # bad image data; fall back to hatman

    with lock:
        if world is None:
            world = WorldState()
        player = PlayerState(world, player_id=player_id, avatar=avatar)
        players.append(player)

    try:
        with conn:
            while True:
                try:
                    msg = recv_json(conn)
                except ConnectionError:
                    break
                update(player, msg.get("keys", {}))
                with lock:
                    others = [p for p in players if p is not player]
                pil_img = renderer.render(player, others)
                raw     = renderer.encode_jpeg(pil_img)
                send_frame(conn, raw)
    finally:
        with lock:
            players.remove(player)


with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    logging.info("attempting websocket connection at: "+str(HOST)+":"+str(PORT))
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((HOST, PORT))
    s.listen()
    while True:
        conn, addr = s.accept()
        threading.Thread(target=handle_client, args=(conn,), daemon=True).start()
