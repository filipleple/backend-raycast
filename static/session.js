// Logs this browser into the game with its 629fm account. 629fm keeps the
// 629Manager session token in localStorage, and the game is served from the
// same origin (629fm.com/raycast/), so we can hand it to the server, which
// checks it with Manager and sets the game's session cookie.
//
// URLs are relative on purpose: the game lives under a path prefix.
const MANAGER_TOKEN_KEY = "629:principalSessionToken";

// startSession resolves to {user} or {error}; never rejects.
async function startSession() {
    let token = null;
    try { token = localStorage.getItem(MANAGER_TOKEN_KEY); } catch (e) {}
    if (!token) return { error: "log in on 629fm to play" };

    try {
        const res = await fetch("session", {
            method: "POST",
            headers: { Authorization: "Bearer " + token },
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) return { error: data.error || `HTTP ${res.status}` };
        return { user: data };
    } catch (e) {
        return { error: "server unreachable" };
    }
}

// Server-sent paths (/ost/..., /picture/...) are root-relative; resolve them
// against the page so they land under the game's prefix.
function gameURL(path) {
    return path.replace(/^\//, "");
}
