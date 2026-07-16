// blessed.js — step into the chapel's rug nook and receive a blessing.
//
// The reference example: invisible 0003 trigger tiles sit at the nook
// entrance (placed in TILES.csv at cols 55/57, row 18), and the altar rugs
// (tile 0210) bless you too if you sneak in sideways. Demonstrates tile-ID
// triggers, per-player script state, popups and synthesized sound.

const lastBlessed = {}; // player id -> timestamp of their last blessing

function bless(ctx) {
    const now = Date.now();
    if (now - (lastBlessed[ctx.player.id] || 0) < 30000) return; // once per 30s
    lastBlessed[ctx.player.id] = now;

    ctx.popup("✦  you have been blessed  ✦", 3500);
    // ascending chime, no audio file needed
    ctx.tone(523.25, 250, 0.30, 0);    // C5
    ctx.tone(659.25, 250, 0.30, 180);  // E5
    ctx.tone(783.99, 500, 0.30, 360);  // G5
    ctx.log(ctx.player.name, "has been blessed at", ctx.col, ctx.row);
}

onEnter("0003", bless); // any trigger tile on the map
onEnter("0210", bless); // stepping straight onto the altar rugs also counts
