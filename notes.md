
* Frame size: 640 by 480
* Square size: fixed side length 80
* Tick rate: fixed interval 50 ms
* Transport framing: 4 byte big endian length prefix for every message on TCP
* One session only for now, even if the Go side can accept multiple


# random ideas

* maybe have two sockets, one for player 1 other for player 2 and try to manage their states independently?
  and manage collision detection in such scenario?
