okay so, i want to load the map layout from map.csv and the definitions from the definitions.csv.

instead of the WALL = 1 EMPTY = 0 etc., each symbol/item on the map has a
corresponding definitions entry. so the map instead of a 2d int array, becomes
an array of items, where the item has such properties as are given in the
definitions file.

the file is pretty intuitive, columns are properties and rows are entries for each symbol.

from the renderer's perspective, what's interesting is:

* is it a wall or a floor or a door?
  - handle doors as doors
  - handle wall as wall
  - handle floor as empty, for now
* what texture do i use for it?
  - the goal is to have the texture name field correspond to a file in the `./textures` folder.
    this means, for example if there's a `1` on the map:
    ```csv
    id,symbol,texture_name,transparency,walk_through,wall,floor,door
    0100,1,stonewall,0,0,1,0,0
    ```
    wall = 1 so it's a wall, so we look in `/textures/walls/` for `stonewall.gif`

    then
    ```csv
    id,symbol,texture_name,transparency,walk_through,wall,floor,door
    0400,D,door,0,0,1,0,1
    ```
    it's a door so we look for door.gif in /textures/doors/door.gif

    so yeah, this replaces the random assignment of a texture

additional info:
* ignore the open door vs closed door for now, let's keep the current door
  handling as portals
* ignore any trait that's not handled by the current renderer mechanics, just
  make a field for it in the symbol structure
