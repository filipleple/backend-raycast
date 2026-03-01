import os
import socket
import math
import random
import threading
from collections import deque
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
EMPTY     = 0
GRID_SIZE = 50

# Colors
GREY    = (200, 200, 200)
RED     = (255, 0, 0)
BROWN   = (100,  60,  20)   # connectivity door on minimap
MAGENTA = (220,   0, 220)   # portal door on minimap

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
HOST = "127.0.0.1"
PORT = 9000

# Asset paths
ASSETS            = os.path.join(os.path.dirname(__file__), '..')
SPRITE_PATH       = os.path.join(ASSETS, 'hatman.gif')
FRAMES_DIR        = os.path.join(ASSETS, 'frames')
TEXTURES_DIR      = os.path.join(ASSETS, 'textures')
DOOR_TEXTURE_PATH = os.path.join(ASSETS, 'textures', 'door', 'door.gif')

# Entity counts per map
NUM_MONSTERS = 0
NUM_FRAMES   = 3

DIRS = [(1, 0), (-1, 0), (0, 1), (0, -1)]


# ---------------------------------------------------------------------------
# Entities
# ---------------------------------------------------------------------------

@dataclass
class Monster:
    x: float
    y: float


@dataclass
class Door:
    """Same-map connectivity door — teleports between two exits on one map."""
    col: int
    row: int
    exit_a: tuple
    exit_b: tuple


class PortalDoor:
    """Cross-map door — leads to a different map with a different wall texture.
    Target map is generated lazily on first use.
    """
    def __init__(self, col, row, exit_pos):
        self.col        = col
        self.row        = row
        self.exit_pos   = exit_pos  # pixel (x, y) on THIS map (where player stands)
        self.target_map = None      # set on first use
        self.target_pos = None      # pixel (x, y) spawn on target map


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


def load_wall_textures(textures_dir):
    """Load textures/walls/*.gif alphabetically → wall type 1, 2, 3…
    Returns the full registry; each Map picks one type to use.
    """
    textures  = {}
    walls_dir = os.path.join(textures_dir, 'walls')
    if os.path.isdir(walls_dir):
        fnames = sorted(f for f in os.listdir(walls_dir) if f.lower().endswith('.gif'))
        for i, fname in enumerate(fnames, start=1):
            textures[i] = Image.open(os.path.join(walls_dir, fname)).convert("RGB")
    return textures


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def find_spawn(m):
    """Pixel (x, y) of the empty cell closest to grid origin."""
    best, best_dist = None, float('inf')
    for row in range(m.rows):
        for col in range(m.cols):
            if m.grid[row][col] == EMPTY:
                dist = math.hypot(col, row)
                if dist < best_dist:
                    best_dist = dist
                    best = (col, row)
    col, row = best
    return (col + 0.5) * m.tile_size, (row + 0.5) * m.tile_size


# ---------------------------------------------------------------------------
# Region connectivity + same-map door placement
# ---------------------------------------------------------------------------

def flood_fill_regions(grid, cols, rows):
    region_of = {}
    count = 0
    for r in range(rows):
        for c in range(cols):
            if grid[r][c] == EMPTY and (c, r) not in region_of:
                q = deque([(c, r)])
                while q:
                    cc, rr = q.popleft()
                    if (cc, rr) in region_of:
                        continue
                    region_of[(cc, rr)] = count
                    for dc, dr in DIRS:
                        nc, nr = cc + dc, rr + dr
                        if 0 <= nc < cols and 0 <= nr < rows \
                                and grid[nr][nc] == EMPTY \
                                and (nc, nr) not in region_of:
                            q.append((nc, nr))
                count += 1
    return region_of, count


def find_doors(grid, cols, rows, tile_size):
    """Return door_cells {(col, row) -> Door} that connect all empty regions."""
    region_of, num_regions = flood_fill_regions(grid, cols, rows)
    if num_regions <= 1:
        return {}

    parent = list(range(num_regions))

    def find(x):
        while parent[x] != x:
            parent[x] = parent[parent[x]]
            x = parent[x]
        return x

    def union(a, b):
        a, b = find(a), find(b)
        if a != b:
            parent[a] = b
            return True
        return False

    def all_connected():
        return len({find(i) for i in range(num_regions)}) == 1

    door_cells = {}

    # Phase 1: single-wall doors
    borders = []
    for r in range(rows):
        for c in range(cols):
            if grid[r][c] != EMPTY:
                adj = {}
                for dc, dr in DIRS:
                    nc, nr = c + dc, r + dr
                    if (nc, nr) in region_of:
                        adj[region_of[(nc, nr)]] = (nc, nr)
                if len(adj) >= 2:
                    borders.append((c, r, adj))
    random.shuffle(borders)

    for c, r, adj in borders:
        rids = sorted(adj)
        for i in range(len(rids)):
            for j in range(i + 1, len(rids)):
                if union(rids[i], rids[j]):
                    ca, cb = adj[rids[i]], adj[rids[j]]
                    door_cells[(c, r)] = Door(c, r,
                        exit_a=((ca[0] + 0.5) * tile_size, (ca[1] + 0.5) * tile_size),
                        exit_b=((cb[0] + 0.5) * tile_size, (cb[1] + 0.5) * tile_size))
        if all_connected():
            return door_cells

    # Phase 2: BFS through walls for thick-wall separations
    while not all_connected():
        comp_members = {}
        for i in range(num_regions):
            comp_members.setdefault(find(i), []).append(i)
        roots    = list(comp_members)
        root_a   = roots[0]
        root_b   = next(r for r in roots if r != root_a)

        seeds = [cell for cell, rid in region_of.items() if find(rid) == root_a]
        prev  = {cell: None for cell in seeds}
        q     = deque(seeds)
        target = None

        while q and target is None:
            cc, rr = q.popleft()
            for dc, dr in DIRS:
                nc, nr = cc + dc, rr + dr
                if not (0 <= nc < cols and 0 <= nr < rows) or (nc, nr) in prev:
                    continue
                prev[(nc, nr)] = (cc, rr)
                q.append((nc, nr))
                cell_rid = region_of.get((nc, nr))
                if cell_rid is not None and find(cell_rid) == root_b:
                    target = (nc, nr)
                    break

        if target is None:
            break

        path = []
        cur = target
        while cur is not None:
            path.append(cur)
            cur = prev[cur]
        path.reverse()

        exit_a_cell = None
        door_pos    = None

        for i, (cc, rr) in enumerate(path):
            if grid[rr][cc] != EMPTY:
                door_pos    = (cc, rr)
                exit_a_cell = path[i - 1] if i > 0 else None
                break

        if door_pos and exit_a_cell:
            dc, dr = door_pos
            door_cells[door_pos] = Door(dc, dr,
                exit_a=((exit_a_cell[0] + 0.5) * tile_size,
                        (exit_a_cell[1] + 0.5) * tile_size),
                exit_b=((target[0] + 0.5) * tile_size,
                        (target[1] + 0.5) * tile_size))
            union(region_of[exit_a_cell], region_of[target])

    return door_cells


# ---------------------------------------------------------------------------
# Map
# ---------------------------------------------------------------------------

class Map:
    """A self-contained room.

    wall_type:         which key in all_wall_textures this map uses.
    wall_textures:     {1: PIL Image} — only the chosen texture, keyed as 1.
    door_cells:        same-map connectivity doors.
    portal_door_cells: cross-map portal doors (one per map).
    """
    def __init__(self, cols, rows, tile_size, grid, wall_type,
                 wall_textures, door_texture, monsters, frame_cells,
                 door_cells, portal_door_cells):
        self.cols              = cols
        self.rows              = rows
        self.tile_size         = tile_size
        self.grid              = grid
        self.wall_type         = wall_type
        self.wall_textures     = wall_textures
        self.door_texture      = door_texture
        self.monsters          = monsters
        self.frame_cells       = frame_cells
        self.door_cells        = door_cells
        self.portal_door_cells = portal_door_cells


def build_map(all_wall_textures, exclude_type=None):
    """Generate a new map, picking a random wall texture different from exclude_type."""
    cols      = WIDTH  // GRID_SIZE
    rows      = HEIGHT // GRID_SIZE
    tile_size = min(WIDTH // cols, HEIGHT // rows)
    grid      = generate_map(cols, rows, fill=0.35, seed=None)

    # pick wall texture
    available = [t for t in all_wall_textures if t != exclude_type]
    if not available:
        available = list(all_wall_textures.keys())
    wall_type     = random.choice(available) if available else None
    map_textures  = {1: all_wall_textures[wall_type]} if wall_type else {}

    # entities
    empty_cells = [(c, r) for r in range(rows) for c in range(cols)
                   if grid[r][c] == EMPTY]
    random.shuffle(empty_cells)

    monsters = [Monster((c + 0.5) * tile_size, (r + 0.5) * tile_size)
                for c, r in empty_cells[:NUM_MONSTERS]]

    frame_images = load_frame_images(FRAMES_DIR)
    frame_cells  = {}
    if frame_images:
        wall_cells = [(c, r) for r in range(rows) for c in range(cols)
                      if grid[r][c] != EMPTY]
        random.shuffle(wall_cells)
        for c, r in wall_cells[:NUM_FRAMES]:
            frame_cells[(c, r)] = random.choice(frame_images).convert("RGB")

    # same-map connectivity doors
    door_cells = find_doors(grid, cols, rows, tile_size)

    # door texture
    door_texture = None
    if os.path.isfile(DOOR_TEXTURE_PATH):
        door_texture = Image.open(DOOR_TEXTURE_PATH).convert("RGB")

    # portal door: one wall cell with at least one empty neighbour,
    # not already used as a connectivity door or picture frame
    used = set(door_cells) | set(frame_cells)
    portal_candidates = []
    for r in range(rows):
        for c in range(cols):
            if grid[r][c] != EMPTY and (c, r) not in used:
                for dc, dr in DIRS:
                    nc, nr = c + dc, r + dr
                    if 0 <= nc < cols and 0 <= nr < rows and grid[nr][nc] == EMPTY:
                        portal_candidates.append((c, r, nc, nr))
                        break
    portal_door_cells = {}
    if portal_candidates:
        pc, pr, ec, er = random.choice(portal_candidates)
        exit_pos = ((ec + 0.5) * tile_size, (er + 0.5) * tile_size)
        portal_door_cells[(pc, pr)] = PortalDoor(pc, pr, exit_pos)

    return Map(cols, rows, tile_size, grid, wall_type,
               map_textures, door_texture, monsters, frame_cells,
               door_cells, portal_door_cells)


# ---------------------------------------------------------------------------
# World
# ---------------------------------------------------------------------------

class WorldState:
    """Holds all maps and the full wall-texture registry.
    New maps are appended when players traverse portal doors.
    """
    def __init__(self):
        self.all_wall_textures = load_wall_textures(TEXTURES_DIR)
        self.maps              = [build_map(self.all_wall_textures)]


# ---------------------------------------------------------------------------
# Player
# ---------------------------------------------------------------------------

class PlayerState:
    def __init__(self, world):
        self.world       = world
        self.current_map = world.maps[0]
        self.cam_angle   = 0.0
        self.show_map    = False
        self.prev_inputs = {}
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

    def encode_png(self, pil_img):
        buf = io.BytesIO()
        pil_img.save(buf, format="PNG")
        return buf.getvalue()

    def draw_wall_map(self, draw, m):
        for y in range(m.rows):
            for x in range(m.cols):
                if m.grid[y][x] == EMPTY:
                    continue
                if (x, y) in m.portal_door_cells:
                    fill = MAGENTA
                elif (x, y) in m.door_cells:
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

            # texture priority: portal door = connectivity door > picture frame > wall > solid
            if (cx, cy) in m.portal_door_cells or (cx, cy) in m.door_cells:
                tex = m.door_texture
            else:
                tex = m.frame_cells.get((cx, cy))
                if tex is None:
                    tex = m.wall_textures.get(m.grid[cy][cx])

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
            [(p.playerX, p.playerY, self.hatman)
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

        conn_door = m.door_cells.get((look_col, look_row))
        if conn_door:
            our_col    = int(player.playerX / m.tile_size)
            our_row    = int(player.playerY / m.tile_size)
            exit_a_col = int(conn_door.exit_a[0] / m.tile_size)
            exit_a_row = int(conn_door.exit_a[1] / m.tile_size)
            if (our_col, our_row) == (exit_a_col, exit_a_row):
                player.playerX, player.playerY = conn_door.exit_b
            else:
                player.playerX, player.playerY = conn_door.exit_a

        portal = m.portal_door_cells.get((look_col, look_row))
        if portal:
            if portal.target_map is None:
                new_map             = build_map(player.world.all_wall_textures,
                                                exclude_type=m.wall_type)
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
        if not (0 <= cx < m.cols and 0 <= cy < m.rows) or m.grid[cy][cx] != EMPTY:
            newX = player.playerX

    if moveY != 0:
        cx = int(newX / ts)
        cy = int((newY + math.copysign(PLAYER_MARGIN, moveY)) / ts)
        if not (0 <= cx < m.cols and 0 <= cy < m.rows) or m.grid[cy][cx] != EMPTY:
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
                update(player, inputs)
                with lock:
                    others = [p for p in players if p is not player]
                pil_img = renderer.render(player, others)
                png     = renderer.encode_png(pil_img)
                send_frame(conn, png)
    finally:
        with lock:
            players.remove(player)


with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((HOST, PORT))
    s.listen()
    while True:
        conn, addr = s.accept()
        threading.Thread(target=handle_client, args=(conn,), daemon=True).start()
