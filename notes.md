
* Frame size: 640 by 480
* Square size: fixed side length 20
* Tick rate: fixed interval 50 ms
* Transport framing: 4 byte big endian length prefix for every message on TCP
* One session only for now, even if the Go side can accept multiple






```python
import numpy as np
import io
from PIL import Image, ImageDraw

# image surface as numpy array
h, w = 480, 640
img = np.zeros((h, w, 3), dtype=np.uint8)

# define rectangle
y0, y1 = 100, 200
x0, x1 = 150, 350

# convert to Pillow canvas
pil_img = Image.fromarray(img, mode="RGB")
draw = ImageDraw.Draw(pil_img)

# draw a rectangle
draw.rectangle([x0, y0, x1, y1], fill=(0, 255, 0))

# store in PNG format
buf = io.BytesIO()
pil_img.save(buf, format="PNG")
png_bytes = buf.getvalue()

# write to disk
with open("out.png", "wb") as f:
    f.write(png_bytes)
```
