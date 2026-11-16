"use strict";

// ---------------------------------------------------------------------------
// État & utilitaires
// ---------------------------------------------------------------------------
const TOKEN_KEY = "douceville_token";
let token = localStorage.getItem(TOKEN_KEY);
let mode = "login"; // "login" | "register"
let map, isoLayer, markerLayer;

const $ = (sel) => document.querySelector(sel);

function setToken(t) {
  token = t;
  if (t) localStorage.setItem(TOKEN_KEY, t);
  else localStorage.removeItem(TOKEN_KEY);
}

// Appel API : ajoute le Bearer token, gère la déconnexion sur 401.
async function api(path, options = {}) {
  const headers = Object.assign(
    { "Content-Type": "application/json" },
    options.headers || {}
  );
  if (token) headers["Authorization"] = "Bearer " + token;

  const res = await fetch("/api" + path, { ...options, headers });
  if (res.status === 401 && token) {
    // Token expiré/invalide → retour à l'écran de connexion.
    setToken(null);
    showAuth();
    throw new Error("Session expirée, reconnectez-vous.");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.detail || `Erreur ${res.status}`);
  }
  return data;
}

// ---------------------------------------------------------------------------
// Écran d'authentification
// ---------------------------------------------------------------------------
function showAuth() {
  $("#app").hidden = true;
  $("#auth").hidden = false;
}

function applyMode() {
  const isRegister = mode === "register";
  $("#auth-submit").textContent = isRegister ? "Créer le compte" : "Se connecter";
  $("#switch-text").textContent = isRegister
    ? "Déjà un compte ?"
    : "Pas encore de compte ?";
  $("#switch-mode").textContent = isRegister ? "Se connecter" : "Créer un compte";
  $("[data-register-only]").hidden = !isRegister;
  $("#password").autocomplete = isRegister ? "new-password" : "current-password";
  $("#auth-error").hidden = true;
}

$("#switch-mode").addEventListener("click", (e) => {
  e.preventDefault();
  mode = mode === "login" ? "register" : "login";
  applyMode();
});

$("#auth-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const err = $("#auth-error");
  err.hidden = true;
  const btn = $("#auth-submit");
  btn.disabled = true;

  const body = {
    email: $("#email").value.trim(),
    password: $("#password").value,
  };
  if (mode === "register") {
    body.first_name = $("#first-name").value.trim() || null;
    body.last_name = $("#last-name").value.trim() || null;
  }

  try {
    const path = mode === "register" ? "/auth/register" : "/auth/login";
    const data = await api(path, { method: "POST", body: JSON.stringify(body) });
    setToken(data.token);
    startApp(data.user);
  } catch (ex) {
    err.textContent = ex.message;
    err.hidden = false;
  } finally {
    btn.disabled = false;
  }
});

// ---------------------------------------------------------------------------
// Application : carte & recherche
// ---------------------------------------------------------------------------
function startApp(user) {
  $("#auth").hidden = true;
  $("#app").hidden = false;

  const name = user && (user.login || user.email);
  $("#user-label").textContent = name || "";

  if (!map) initMap();
  // Leaflet a besoin de recalculer sa taille une fois le conteneur visible.
  setTimeout(() => map.invalidateSize(), 0);
}

function initMap() {
  map = L.map("map").setView([46.6, 2.5], 6); // France
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: "&copy; OpenStreetMap",
  }).addTo(map);
  isoLayer = L.layerGroup().addTo(map);
  markerLayer = L.layerGroup().addTo(map);
}

$("#time").addEventListener("input", (e) => {
  $("#time-val").textContent = e.target.value;
});

$("#logout").addEventListener("click", () => {
  setToken(null);
  showAuth();
  applyMode();
});

function checkedValues(name) {
  return Array.from(
    document.querySelectorAll(`input[name="${name}"]:checked`)
  ).map((el) => el.value);
}

$("#search-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const status = $("#search-status");
  const btn = $("#search-btn");
  status.classList.remove("err");
  status.textContent = "Recherche en cours…";
  btn.disabled = true;

  const body = {
    adresse: $("#address").value.trim(),
    dist: parseInt($("#time").value, 10) * 60, // minutes → secondes
    transp: $("#transp").value,
    nature: checkedValues("nature"),
    secteur: checkedValues("secteur"),
  };

  try {
    const data = await api("/etablissements", {
      method: "POST",
      body: JSON.stringify(body),
    });
    render(data);
    status.textContent = `${data.etablissements ? data.etablissements.length : 0} établissement(s) trouvé(s).`;
  } catch (ex) {
    status.textContent = ex.message;
    status.classList.add("err");
  } finally {
    btn.disabled = false;
  }
});

function render(data) {
  isoLayer.clearLayers();
  markerLayer.clearLayers();

  // Isochrone : geometry = [[lon, lat], ...] → Leaflet attend [lat, lon].
  const iso = data.isochrone;
  if (iso && Array.isArray(iso.geometry) && iso.geometry.length) {
    const ring = iso.geometry.map(([lon, lat]) => [lat, lon]);
    const poly = L.polygon(ring, {
      color: "#38bdf8",
      weight: 2,
      fillColor: "#38bdf8",
      fillOpacity: 0.12,
    });
    isoLayer.addLayer(poly);
    if (iso.lonlat) {
      L.circleMarker([iso.lonlat[1], iso.lonlat[0]], {
        radius: 7,
        color: "#a855f7",
        fillColor: "#a855f7",
        fillOpacity: 0.9,
      })
        .bindPopup("Point de départ")
        .addTo(isoLayer);
    }
  }

  const etabs = data.etablissements || [];
  const bounds = [];
  for (const etab of etabs) {
    const coords = etab.position && etab.position.coordinates;
    if (!coords) continue;
    const [lon, lat] = coords;
    bounds.push([lat, lon]);
    L.marker([lat, lon]).bindPopup(popupHtml(etab)).addTo(markerLayer);
  }

  if (bounds.length) {
    map.fitBounds(bounds, { padding: [40, 40], maxZoom: 14 });
  } else if (iso && iso.lonlat) {
    map.setView([iso.lonlat[1], iso.lonlat[0]], 12);
  }
}

function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"]/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])
  );
}

function popupHtml(etab) {
  let html = `<div class="popup-title">${esc(etab.nom)}</div>`;
  const meta = [etab.nature, etab.secteur].filter(Boolean).join(" · ");
  const addr = [etab.adresse, etab.code_postal, etab.commune].filter(Boolean).join(", ");
  html += `<div class="popup-meta">${esc(meta)}<br/>${esc(addr)}`;
  if (etab.UAI) html += `<br/>UAI : ${esc(etab.UAI)}`;
  html += `</div>`;

  const res = etab.resultats || [];
  if (res.length) {
    html += `<table class="popup-table"><thead><tr>
      <th>Diplôme</th><th>Année</th><th>Présents</th><th>Admis</th><th>Mentions</th>
      </tr></thead><tbody>`;
    for (const r of res.slice(0, 8)) {
      html += `<tr><td>${esc(r.diplome)}</td><td>${esc(r.annee)}</td>
        <td>${esc(r.presents)}</td><td>${esc(r.admis)}</td><td>${esc(r.mentions)}</td></tr>`;
    }
    html += `</tbody></table>`;
  } else {
    html += `<em>Aucun résultat d'examen disponible.</em>`;
  }
  return html;
}

// ---------------------------------------------------------------------------
// Démarrage : session existante ?
// ---------------------------------------------------------------------------
(async function boot() {
  applyMode();
  if (token) {
    try {
      const user = await api("/user");
      startApp(user);
      return;
    } catch (_) {
      setToken(null);
    }
  }
  showAuth();
})();
