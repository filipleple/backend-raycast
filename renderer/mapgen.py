import random
import csv
import os

ASSETS            = os.path.join(os.path.dirname(__file__), '..')
MAP_PATH          = os.path.join(ASSETS, 'map.csv')

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

def load_map(cols, rows):
    grid = list(csv.reader(open(MAP_PATH)))

    for y in range(rows):
        for x in range(cols):
            if int(grid[y][x]) == 1:
                grid[y][x] = WALL
            else:
                grid[y][x] = EMPTY
    return grid
