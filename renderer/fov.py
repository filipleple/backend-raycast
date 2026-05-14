from math import cos, sin, radians
from dda import cast_ray_dda

def cast_fov(grid, cols, rows, tile_size, ox, oy, cam_angle, fov_deg, num_rays):
    fov   = radians(fov_deg)
    start = cam_angle - fov / 2
    step  = fov / (num_rays - 1)

    hits_of_each_ray = []

    for i in range(num_rays):
        ang = start + i * step
        dx  = cos(ang)
        dy  = sin(ang)
        hits_of_each_ray.append(cast_ray_dda(grid, cols, rows, tile_size, ox, oy, dx, dy))

    return hits_of_each_ray
