import random
import csv
import os
import math
from collections import deque
from dataclasses import dataclass, field

ASSETS           = os.path.join(os.path.dirname(__file__), '..')
MAP_PATH         = os.path.join(ASSETS, 'map.csv')
DEFINITIONS_PATH = os.path.join(ASSETS, 'definitions.csv')

# kept for generate_map (procedural path)
EMPTY = 0
WALL  = 1

DIRS = [(1, 0), (-1, 0), (0, 1), (0, -1)]


@dataclass
class Symbol:
    id:           str
    symbol:       str
    texture_name: str
    transparency: bool
    walk_through: bool
    wall:         bool
    floor:        bool
    door:         bool


@dataclass
class Door:
    """Same-map connectivity door — teleports between two exits on one map."""
    col:    int
    row:    int
    exit_a: tuple
    exit_b: tuple


class PortalDoor:
    """Cross-map door — leads to a different map with a different wall texture.
    Target map is generated lazily on first use.
    """
    def __init__(self, col, row, exit_pos):
        self.col        = col
        self.row        = row
        self.exit_pos   = exit_pos
        self.target_map = None
        self.target_pos = None


def load_definitions():
    """Return {symbol_str: Symbol} from definitions.csv."""
    defs = {}
    with open(DEFINITIONS_PATH, newline='') as f:
        for row in csv.DictReader(f):
            sym = Symbol(
                id=row['id'],
                symbol=row['symbol'],
                texture_name=row['texture_name'],
                transparency=row['transparency'] == '1',
                walk_through=row['walk_through'] == '1',
                wall=row['wall'] == '1',
                floor=row['floor'] == '1',
                door=row['door'] == '1',
            )
            defs[row['symbol']] = sym
    return defs


_FALLBACK_FLOOR = Symbol(
    id='', symbol='', texture_name='',
    transparency=True, walk_through=True,
    wall=False, floor=True, door=False,
)


def load_map():
    """Load map.csv using definitions.csv.

    Returns (grid, door_positions, cols, rows) where grid is [[Symbol]].
    Unknown symbols are treated as floor.
    """
    defs = load_definitions()
    raw  = list(csv.reader(open(MAP_PATH)))
    rows = len(raw)
    cols = next((i for i, v in enumerate(raw[0]) if v.strip() == ''), len(raw[0]))

    door_positions = []
    grid = []
    for y in range(rows):
        row = []
        for x in range(cols):
            val = raw[y][x].strip()
            sym = defs.get(val, _FALLBACK_FLOOR)
            if sym.door:
                door_positions.append((x, y))
            row.append(sym)
        grid.append(row)
    return grid, door_positions, cols, rows


def generate_map(cols, rows, fill=0.3, seed=None):
    """Procedural int grid — kept for future use."""
    rng  = random.Random(seed)
    grid = [[EMPTY for _ in range(cols)] for _ in range(rows)]
    for y in range(rows):
        for x in range(cols):
            if x == 0 or y == 0 or x == cols - 1 or y == rows - 1:
                grid[y][x] = WALL
            elif rng.random() < fill:
                grid[y][x] = WALL
    return grid


def find_spawn(m):
    """Pixel (x, y) of the empty cell closest to grid origin."""
    best, best_dist = None, float('inf')
    for row in range(m.rows):
        for col in range(m.cols):
            if m.grid[row][col].floor:
                dist = math.hypot(col, row)
                if dist < best_dist:
                    best_dist = dist
                    best      = (col, row)
    return (3 + 0.5) * m.tile_size, (3 + 0.5) * m.tile_size


def flood_fill_regions(grid, cols, rows):
    region_of = {}
    count     = 0
    for r in range(rows):
        for c in range(cols):
            if grid[r][c].floor and (c, r) not in region_of:
                q = deque([(c, r)])
                while q:
                    cc, rr = q.popleft()
                    if (cc, rr) in region_of:
                        continue
                    region_of[(cc, rr)] = count
                    for dc, dr in DIRS:
                        nc, nr = cc + dc, rr + dr
                        if 0 <= nc < cols and 0 <= nr < rows \
                                and grid[nr][nc].floor \
                                and (nc, nr) not in region_of:
                            q.append((nc, nr))
                count += 1
    return region_of, count


def make_csv_doors(door_positions, grid, cols, rows, tile_size):
    """Build Door objects from cells marked door=1 in definitions."""
    door_cells = {}
    for c, r in door_positions:
        exits = []
        for dc, dr in DIRS:
            nc, nr = c + dc, r + dr
            if 0 <= nc < cols and 0 <= nr < rows and grid[nr][nc].floor:
                exits.append((nc, nr))
        if len(exits) >= 2:
            ca, cb = exits[0], exits[1]
            door_cells[(c, r)] = Door(c, r,
                exit_a=((ca[0] + 0.5) * tile_size, (ca[1] + 0.5) * tile_size),
                exit_b=((cb[0] + 0.5) * tile_size, (cb[1] + 0.5) * tile_size))
    return door_cells


def find_doors(grid, cols, rows, tile_size):
    """Return door_cells {(col, row) -> Door} connecting all empty regions."""
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

    borders = []
    for r in range(rows):
        for c in range(cols):
            if not grid[r][c].floor:
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

    while not all_connected():
        comp_members = {}
        for i in range(num_regions):
            comp_members.setdefault(find(i), []).append(i)
        roots  = list(comp_members)
        root_a = roots[0]
        root_b = next(r for r in roots if r != root_a)

        seeds  = [cell for cell, rid in region_of.items() if find(rid) == root_a]
        prev   = {cell: None for cell in seeds}
        q      = deque(seeds)
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
        cur  = target
        while cur is not None:
            path.append(cur)
            cur = prev[cur]
        path.reverse()

        exit_a_cell = None
        door_pos    = None

        for i, (cc, rr) in enumerate(path):
            if not grid[rr][cc].floor:
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
