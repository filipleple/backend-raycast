import random
import csv
import os

ASSETS            = os.path.join(os.path.dirname(__file__), '..')
MAP_PATH          = os.path.join(ASSETS, 'map.csv')

EMPTY = 0
WALL = 1
DOOR = 1

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
    door_positions = []

    for y in range(rows):
        for x in range(cols):
            val = int(grid[y][x])
            if val == 2:
                door_positions.append((x, y))
                grid[y][x] = WALL
            elif val == 1:
                grid[y][x] = WALL
            else:
                grid[y][x] = EMPTY
    return grid, door_positions
