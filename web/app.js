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
let lastData = null; // dernière réponse de /etablissements (re-filtrage côté client)

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
  lastData = null;
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

  lastData = data;
  const bounds = renderMarkers();

  if (bounds.length) {
    map.fitBounds(bounds, { padding: [40, 40], maxZoom: 14 });
  } else if (iso && iso.lonlat) {
    map.setView([iso.lonlat[1], iso.lonlat[0]], 12);
  }
}

// (Re)dessine les marqueurs de la dernière recherche selon le filtre de taux
// de réussite ; renvoie les positions affichées.
function renderMarkers() {
  const bounds = [];
  if (!markerLayer || !lastData) return bounds;
  markerLayer.clearLayers();

  const all = lastData.etablissements || [];
  const etabs = all.filter(passesRateFilter);
  for (const etab of etabs) {
    const coords = etab.position && etab.position.coordinates;
    if (!coords) continue;
    const [lon, lat] = coords;
    bounds.push([lat, lon]);
    L.marker([lat, lon]).bindPopup(popupHtml(etab)).addTo(markerLayer);
  }

  const status = $("#search-status");
  status.classList.remove("err");
  status.textContent =
    etabs.length === all.length
      ? `${all.length} établissement(s) trouvé(s).`
      : `${etabs.length} établissement(s) affiché(s) sur ${all.length} trouvé(s).`;
  return bounds;
}

// ---------------------------------------------------------------------------
// Filtre « taux de réussite » (Admis / Présents) : slicer + champs synchronisés
// ---------------------------------------------------------------------------

// Taux global d'un établissement en % (Σ admis / Σ présents), ou null.
function successRate(etab) {
  let presents = 0;
  let admis = 0;
  for (const r of etab.resultats || []) {
    if (r.presents == null || r.admis == null || r.presents <= 0) continue;
    presents += r.presents;
    admis += r.admis;
  }
  return presents > 0 ? (100 * admis) / presents : null;
}

function rateBounds() {
  return {
    min: parseInt($("#rate-min").value, 10),
    max: parseInt($("#rate-max").value, 10),
  };
}

// Plage complète (0–100) : aucun filtrage. Sinon, les établissements sans
// résultats d'examen sont masqués.
function passesRateFilter(etab) {
  const { min, max } = rateBounds();
  if (min <= 0 && max >= 100) return true;
  const rate = successRate(etab);
  return rate != null && rate >= min && rate <= max;
}

function syncRateUI() {
  const { min, max } = rateBounds();
  $("#rate-min-txt").value = min;
  $("#rate-max-txt").value = max;
  const fill = $("#rate-fill");
  fill.style.left = min + "%";
  fill.style.right = 100 - max + "%";
  // Poignées confondues : garder accessible celle qui peut encore bouger.
  $("#rate-min").style.zIndex = min === max && min >= 50 ? 2 : "";
}

function setRate(which, value) {
  const { min, max } = rateBounds();
  let v = Math.round(Math.min(100, Math.max(0, value)));
  if (which === "min") v = Math.min(v, max);
  else v = Math.max(v, min);
  $(`#rate-${which}`).value = v;
}

function onRateChange() {
  syncRateUI();
  renderMarkers();
}

for (const which of ["min", "max"]) {
  const slider = $(`#rate-${which}`);
  const txt = $(`#rate-${which}-txt`);

  slider.addEventListener("input", () => {
    setRate(which, parseInt(slider.value, 10)); // empêche min > max
    onRateChange();
  });

  // Saisie en cours : on ne met à jour que si la valeur est utilisable,
  // sans réécrire le champ pour ne pas gêner la frappe.
  txt.addEventListener("input", () => {
    const v = parseFloat(txt.value);
    if (Number.isNaN(v)) return;
    setRate(which, v);
    const { min, max } = rateBounds();
    const fill = $("#rate-fill");
    fill.style.left = min + "%";
    fill.style.right = 100 - max + "%";
    renderMarkers();
  });

  // Validation (Entrée / perte du focus) : le champ reprend la valeur retenue.
  txt.addEventListener("change", () => {
    const v = parseFloat(txt.value);
    if (!Number.isNaN(v)) setRate(which, v);
    onRateChange();
  });
}
syncRateUI();

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
  const rate = successRate(etab);
  if (rate != null) {
    html += `<div class="popup-meta">Taux de réussite : <strong>${rate.toFixed(1)} %</strong></div>`;
  }
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
