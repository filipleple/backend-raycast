import random

WALL = 1
EMPTY = 0

def generate_map(cols, rows, fill=0.3, seed=None):
    rng = random.Random(seed)
    grid = [[EMPTY for _ in range(cols)] for _ in range(rows)]

    for y in range(rows):
        for x in range(cols):
            if x == 0 or y == 0 or x == cols-1 or y == rows-1:
                grid[y][x] = WALL
            elif rng.random() < fill:
                grid[y][x] = WALL
    return grid
