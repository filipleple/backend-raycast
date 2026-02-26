from math import cos, sin, radians
from dda import cast_ray_dda

def cast_fov(grid, cols, rows, tile_size, ox, oy, cam_angle, fov_deg, num_rays):
    fov = radians(fov_deg)
    start = cam_angle - fov / 2
    step = fov / (num_rays - 1)

    dists = [0.0] * num_rays
    sides = [0] * num_rays

    for i in range(num_rays):
        ang = start + i * step
        dx = cos(ang)
        dy = sin(ang)
        hit, dist, side = cast_ray_dda(grid, cols, rows, tile_size, ox, oy, dx, dy)
        dists[i] = dist
        sides[i] = side if side is not None else 0

    return dists, sides
