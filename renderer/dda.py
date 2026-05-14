from math import inf

def cast_ray_dda(grid, cols, rows, tile_size, ox, oy, dx, dy):
    mapX = int(ox // tile_size)
    mapY = int(oy // tile_size)

    if 0 <= mapX < cols and 0 <= mapY < rows and not grid[mapY][mapX].floor:
        return []

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

    one_rays_hits = []

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
            break

        if not grid[mapY][mapX].floor:
            dist = sideDistX - deltaDistX if side == 0 else sideDistY - deltaDistY
            # UV: fractional position along the wall face that was hit
            if side == 0:
                u = (oy + dist * dy) / tile_size % 1.0
            else:
                u = (ox + dist * dx) / tile_size % 1.0

            if (grid[mapY][mapX].transparency):
                one_rays_hits.append((dist, side, u, mapX, mapY))
                continue
            else:
                one_rays_hits.append((dist, side, u, mapX, mapY))
                break

    return one_rays_hits
