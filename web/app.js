"use strict";

// ---------------------------------------------------------------------------
// État & utilitaires
// ---------------------------------------------------------------------------
const TOKEN_KEY = "douceville_token";
let token = localStorage.getItem(TOKEN_KEY);
let mode = "login"; // "login" | "register"
const PENDING_MSG =
  "Votre compte est en attente de validation par un administrateur. " +
  "Vous pourrez vous connecter dès qu'il aura été approuvé.";
let pendingEmail = null; // e-mail en attente de vérification (pour le renvoi du lien)
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
  if (res.status === 403 && data.code === "account_not_granted" && token) {
    // Accès retiré par un administrateur → retour à l'écran de connexion.
    setToken(null);
    showAuth();
    showInfo(PENDING_MSG, false);
    throw new Error(PENDING_MSG);
  }
  if (!res.ok) {
    const err = new Error(data.detail || `Erreur ${res.status}`);
    err.status = res.status;
    err.code = data.code;
    throw err;
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
  $("#auth-info").hidden = true;
}

function showError(message) {
  $("#auth-info").hidden = true;
  const err = $("#auth-error");
  err.textContent = message;
  err.hidden = false;
}

// Affiche un message d'information, avec éventuellement le lien « Renvoyer ».
function showInfo(message, withResend) {
  $("#auth-error").hidden = true;
  $("#auth-info-text").textContent = message;
  $("#resend-link").hidden = !withResend;
  $("#auth-info").hidden = false;
}

$("#resend-link").addEventListener("click", async (e) => {
  e.preventDefault();
  const email = pendingEmail || $("#email").value.trim();
  if (!email) return;
  try {
    await api("/auth/resend-verification", {
      method: "POST",
      body: JSON.stringify({ email }),
    });
    showInfo(`Si un compte non vérifié existe pour ${email}, un nouveau lien vient d'être envoyé.`, false);
  } catch (ex) {
    showError(ex.message);
  }
});

$("#switch-mode").addEventListener("click", (e) => {
  e.preventDefault();
  mode = mode === "login" ? "register" : "login";
  applyMode();
});

$("#auth-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("#auth-error").hidden = true;
  $("#auth-info").hidden = true;
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
    if (mode === "register") {
      const data = await api("/auth/register", { method: "POST", body: JSON.stringify(body) });
      pendingEmail = data.email || body.email;
      mode = "login";
      applyMode();
      $("#password").value = "";
      showInfo(
        `Compte créé ! Un e-mail de confirmation a été envoyé à ${pendingEmail}. ` +
          "Cliquez sur le lien qu'il contient, puis attendez la validation de votre compte par un administrateur.",
        true
      );
    } else {
      const data = await api("/auth/login", { method: "POST", body: JSON.stringify(body) });
      setToken(data.token);
      startApp(data.user);
    }
  } catch (ex) {
    if (ex.code === "email_not_verified") {
      pendingEmail = body.email;
      showInfo(
        "Votre adresse e-mail n'est pas encore vérifiée. Consultez votre boîte de réception.",
        true
      );
    } else if (ex.code === "account_not_granted") {
      showInfo(PENDING_MSG, false);
    } else {
      showError(ex.message);
    }
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

$("#logout").addEventListener("click", async () => {
  // Révoque le token côté serveur ; on se déconnecte localement quoi qu'il arrive.
  try {
    if (token) await api("/auth/logout", { method: "POST" });
  } catch (_) {
    /* token déjà invalide ou serveur injoignable */
  }
  setToken(null);
  if (isoLayer) isoLayer.clearLayers();
  if (markerLayer) markerLayer.clearLayers();
  $("#results-summary").hidden = true;
  $("#search-status").textContent = "";
  $("#password").value = "";
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

  let verifyFailed = false;
  let verifiedPending = false;

  // Lien de vérification reçu par e-mail : /verify-email?token=...
  if (location.pathname === "/verify-email") {
    const verifyToken = new URLSearchParams(location.search).get("token");
    // Retire le jeton de l'URL (historique, partage d'écran…).
    history.replaceState(null, "", "/");
    if (verifyToken) {
      try {
        const data = await api("/auth/verify", {
          method: "POST",
          body: JSON.stringify({ token: verifyToken }),
        });
        if (data.token) {
          // Compte déjà autorisé : connexion directe.
          setToken(data.token);
          startApp(data.user);
          return;
        }
        verifiedPending = true;
      } catch (_) {
        verifyFailed = true;
      }
    }
  }

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
  if (verifiedPending) {
    showInfo("Adresse e-mail vérifiée, merci ! " + PENDING_MSG, false);
  } else if (verifyFailed) {
    showInfo(
      "Ce lien de vérification est invalide ou a expiré. Connectez-vous pour en recevoir un nouveau.",
      false
    );
  }
})();
