from math import inf

def cast_ray_dda(grid, cols, rows, tile_size, ox, oy, dx, dy, WALL=1):
    mapX = int(ox // tile_size)
    mapY = int(oy // tile_size)

    if 0 <= mapX < cols and 0 <= mapY < rows and grid[mapY][mapX] == WALL:
        return True, 0.0, None

    if dx == 0:
        stepX = 0
        sideDistX = inf
        deltaDistX = inf
    else:
        deltaDistX = abs(tile_size / dx)
        if dx < 0:
            stepX = -1
            sideDistX = (ox - mapX * tile_size) / abs(dx)
        else:
            stepX = 1
            sideDistX = ((mapX + 1) * tile_size - ox) / abs(dx)

    if dy == 0:
        stepY = 0
        sideDistY = inf
        deltaDistY = inf
    else:
        deltaDistY = abs(tile_size / dy)
        if dy < 0:
            stepY = -1
            sideDistY = (oy - mapY * tile_size) / abs(dy)
        else:
            stepY = 1
            sideDistY = ((mapY + 1) * tile_size - oy) / abs(dy)

    while True:
        if sideDistX < sideDistY:
            sideDistX += deltaDistX
            mapX += stepX
            side = 0
        else:
            sideDistY += deltaDistY
            mapY += stepY
            side = 1

        if mapX < 0 or mapX >= cols or mapY < 0 or mapY >= rows:
            return False, inf, side

        if grid[mapY][mapX] == WALL:
            dist = sideDistX - deltaDistX if side == 0 else sideDistY - deltaDistY
            return True, dist, side
