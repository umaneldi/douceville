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
let map, isoLayer, markerLayer, communeLayer, immoLayer;
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
  // Choroplèthe dans un volet sous l'isochrone et les marqueurs.
  map.createPane("communes").style.zIndex = 350;
  communeLayer = L.geoJSON(null, {
    pane: "communes",
    style: communeStyle,
    onEachFeature: (f, layer) => layer.bindTooltip(communeTooltip(f.properties), { sticky: true }),
  }).addTo(map);
  // Prix au m² : même niveau que la sécurité (les deux sont exclusives).
  map.createPane("immobilier").style.zIndex = 345;
  immoLayer = L.geoJSON(null, {
    pane: "immobilier",
    style: immoStyle,
    onEachFeature: (f, layer) => layer.bindTooltip(immoTooltip(f.properties), { sticky: true }),
  }); // désactivée par défaut
  isoLayer = L.layerGroup().addTo(map);
  markerLayer = L.layerGroup().addTo(map);
  const layers = L.control.layers(
    null,
    { [securityLayerLabel()]: communeLayer, [immoLayerLabel()]: immoLayer },
    { collapsed: false },
  );
  layers.addTo(map);
  // L'icône d'info est dans le <label> de la case : son clic ne doit pas
  // basculer la couche (le survol / focus affiche l'infobulle).
  layers.getContainer().querySelectorAll(".info-tip").forEach((tip) =>
    tip.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
    }),
  );
  securityLegend().addTo(map);
  immoLegend().addTo(map);
  map.on("overlayadd overlayremove", (e) => {
    const added = e.type === "overlayadd";
    if (e.layer === communeLayer) $(".security-legend").hidden = !added;
    if (e.layer === immoLayer) {
      if (added) restyleImmobilier();
      else $(".immo-legend").hidden = true;
    }
    // Les deux choroplèthes se superposent mal : en activer une masque l'autre.
    // Différé : pendant le clic, le contrôle des couches ne se met pas à jour.
    const other = e.layer === communeLayer ? immoLayer : e.layer === immoLayer ? communeLayer : null;
    if (added && other) setTimeout(() => map.removeLayer(other), 0);
  });
  // Niveau de détail : communes de loin, quartiers IRIS de près.
  map.on("zoomend", () => {
    if (immoData && immoLevelForZoom() !== immoLevel) buildImmobilier();
  });
  // Échelle dynamique : recalculée sur la zone visible.
  map.on("moveend", () => {
    if (immoData && map.hasLayer(immoLayer)) restyleImmobilier();
  });
}

// ---------------------------------------------------------------------------
// Choroplèthe « sécurité des communes » (score 0–100, 100 = plus sûre)
// ---------------------------------------------------------------------------
// Rampe séquentielle bleue, du clair (moins sûre) au foncé (plus sûre).
const SECURITY_CLASSES = [
  { min: 80, color: "#0d366b", label: "80–100 (plus sûre)" },
  { min: 60, color: "#1c5cab", label: "60–79" },
  { min: 40, color: "#3987e5", label: "40–59" },
  { min: 20, color: "#86b6ef", label: "20–39" },
  { min: 0, color: "#cde2fb", label: "0–19 (moins sûre)" },
];
const NO_DATA_COLOR = "#9a9a96";

// Indicateurs SSMSI entrant dans le score, par poids. Doit rester aligné sur la
// CTE `poids` de internal/store/migrations/009_securite_score.sql.
const SECURITY_INDICATORS = [
  {
    poids: 3,
    noms: [
      "Violences physiques hors cadre familial",
      "Violences physiques intrafamiliales",
      "Violences sexuelles",
      "Vols avec armes",
      "Vols violents sans arme",
      "Cambriolages de logement",
    ],
  },
  {
    poids: 2,
    noms: [
      "Destructions et dégradations volontaires",
      "Vols de véhicule",
      "Vols dans les véhicules",
      "Vols d'accessoires sur véhicules",
      "Vols sans violence contre des personnes",
    ],
  },
  {
    poids: 1,
    noms: [
      "Escroqueries et fraudes aux moyens de paiement",
      "Trafic de stupéfiants",
      "Usage de stupéfiants",
      "Usage de stupéfiants (AFD)",
    ],
  },
];

// Libellé de la couche dans le contrôle : nom + icône d'info avec infobulle
// listant les indicateurs pris en compte et leur poids.
function securityLayerLabel() {
  const groups = SECURITY_INDICATORS.map(
    (g) =>
      `<div class="tip-group"><div class="tip-weight">Poids ${g.poids}</div><ul>` +
      g.noms.map((n) => `<li>${esc(n)}</li>`).join("") +
      `</ul></div>`,
  ).join("");
  return (
    `Sécurité des communes ` +
    `<span class="info-tip" tabindex="0" role="button" aria-label="Indicateurs du score">i` +
    `<span class="tip-body" role="tooltip">` +
    `<strong>Indicateurs du score</strong>` +
    `<p>Taux pour 1 000 habitants (SSMSI, dernière année), classés par rapport à ` +
    `toutes les communes puis moyennés selon leur poids. Seuls les indicateurs ` +
    `diffusés par la commune comptent.</p>${groups}</span></span>`
  );
}

function securityColor(score) {
  if (score == null) return NO_DATA_COLOR;
  return SECURITY_CLASSES.find((c) => score >= c.min).color;
}

function communeStyle(feature) {
  const score = feature.properties.score;
  return {
    color: "#ffffff",
    weight: 1,
    opacity: 0.9,
    fillColor: securityColor(score),
    fillOpacity: score == null ? 0.35 : 0.6,
    dashArray: score == null ? "3 3" : null,
  };
}

function communeTooltip(p) {
  const nom = `<strong>${esc(p.nom)}</strong>`;
  if (p.score == null) return `${nom}<br>Sécurité : pas de données diffusées`;
  return (
    `${nom}<br>Sécurité : <strong>${p.score}/100</strong>` +
    `<br><span class="muted">plus sûre que ${p.score} % des communes` +
    `<br>${p.annee}, ${p.nb_indicateurs}/15 indicateurs diffusés</span>`
  );
}

function securityLegend() {
  const legend = L.control({ position: "bottomright" });
  legend.onAdd = () => {
    const div = L.DomUtil.create("div", "map-legend security-legend");
    const rows = SECURITY_CLASSES.map(
      (c) => `<li><span class="swatch" style="background:${c.color}"></span>${c.label}</li>`,
    ).join("");
    div.innerHTML =
      `<div class="legend-title">Score de sécurité</div><ul>${rows}` +
      `<li><span class="swatch nodata"></span>Pas de données</li></ul>`;
    div.hidden = true; // affichée dès qu'une recherche renvoie des communes
    return div;
  };
  return legend;
}

function renderCommunes(communes) {
  communeLayer.clearLayers();
  const features = (communes || [])
    .filter((c) => c.contour)
    .map((c) => ({
      type: "Feature",
      geometry: c.contour,
      properties: {
        code_commune: c.code_commune,
        nom: c.nom,
        score: c.score,
        annee: c.annee,
        nb_indicateurs: c.nb_indicateurs,
      },
    }));
  communeLayer.addData(features);
  $(".security-legend").hidden = !features.length || !map.hasLayer(communeLayer);
}

// ---------------------------------------------------------------------------
// Choroplèthe « prix au m² » (DVF, dernier millésime)
// ---------------------------------------------------------------------------
// En dessous de ce zoom, les prix sont agrégés par commune ; au-delà, détail
// par quartier IRIS.
const IRIS_MIN_ZOOM = 12;
// Rampe séquentielle orange/brun, du moins cher (clair) au plus cher (foncé).
const IMMO_COLORS = ["#fdeed4", "#f9c98b", "#ee9a4a", "#c8651b", "#7f3b08"];
let immoData = null; // data.immobilier de la dernière recherche
let immoCommunes = []; // data.communes (noms et contours des communes)
let immoLevel = null; // "commune" | "iris" (niveau actuellement affiché)
let immoScale = null; // { breaks, colors, min, max } de l'échelle courante

const fmtPrix = (n) => `${n.toLocaleString("fr-FR")} €/m²`;

function immoLayerLabel() {
  return (
    `Prix au m² ` +
    `<span class="info-tip" tabindex="0" role="button" aria-label="Source des prix">i` +
    `<span class="tip-body" role="tooltip">` +
    `<strong>Prix au m² des biens bâtis</strong>` +
    `<p>Ventes DVF géolocalisées (data.gouv.fr) d'un seul bien bâti : prix médian ` +
    `de la valeur foncière rapportée à la surface bâtie, tous types de biens ` +
    `confondus.</p>` +
    `<p>De loin, chaque commune affiche la médiane de toutes ses ventes ; zoomez ` +
    `pour le détail par quartier IRIS.</p>` +
    `<p>Les couleurs sont réparties en quantiles des prix de la zone visible.</p>` +
    `</span></span>`
  );
}

function immoLevelForZoom() {
  return map.getZoom() >= IRIS_MIN_ZOOM ? "iris" : "commune";
}

// Prix au m² médian d'une entité, ou null sans vente.
function prixFor(p) {
  return p.prix ? p.prix.prix_m2 : null;
}

// Quantiles (au plus 5 classes, bornes arrondies à 100 €) des prix des entités
// visibles, ou de toutes si aucune n'est visible.
function computeImmoScale() {
  const view = map.getBounds();
  const all = [];
  let visible = [];
  immoLayer.eachLayer((l) => {
    const v = prixFor(l.feature.properties);
    if (v == null) return;
    all.push(v);
    if (view.intersects(l.getBounds())) visible.push(v);
  });
  const vals = (visible.length ? visible : all).sort((a, b) => a - b);
  if (!vals.length) return null;
  const n = IMMO_COLORS.length;
  const breaks = [];
  for (let i = 1; i < n; i++) {
    const b = Math.round(vals[Math.floor((i * vals.length) / n)] / 100) * 100;
    if (b > vals[0] && b <= vals[vals.length - 1] && b > (breaks[breaks.length - 1] ?? -Infinity)) {
      breaks.push(b);
    }
  }
  // Couleurs réparties sur toute la rampe quel que soit le nombre de classes.
  const k = breaks.length + 1;
  const colors = Array.from({ length: k }, (_, i) =>
    IMMO_COLORS[k === 1 ? Math.floor(n / 2) : Math.round((i * (n - 1)) / (k - 1))],
  );
  return { breaks, colors, min: vals[0], max: vals[vals.length - 1] };
}

function immoColor(v) {
  if (v == null || !immoScale) return NO_DATA_COLOR;
  return immoScale.colors[immoScale.breaks.filter((b) => v >= b).length];
}

function immoStyle(feature) {
  const v = prixFor(feature.properties);
  return {
    color: "#ffffff",
    weight: feature.properties.niveau === "iris" ? 0.6 : 1,
    opacity: 0.9,
    fillColor: immoColor(v),
    fillOpacity: v == null ? 0.35 : 0.65,
    dashArray: v == null ? "3 3" : null,
  };
}

function prixLine(p) {
  if (!p) return `Prix médian : <span class="muted">pas de vente</span>`;
  const ventes = `${p.ventes.toLocaleString("fr-FR")} vente${p.ventes > 1 ? "s" : ""}`;
  return `Prix médian : <strong>${fmtPrix(p.prix_m2)}</strong> <span class="muted">(${ventes})</span>`;
}

function immoTooltip(p) {
  const commune = p.niveau === "commune";
  const sub = commune ? "" : `<br><span class="muted">Quartier IRIS — ${esc(p.commune || "")}</span>`;
  return (
    `<strong>${esc(p.nom)}</strong>${sub}` +
    `<br>${prixLine(p.prix)}`
  );
}

function immoLegend() {
  const legend = L.control({ position: "bottomright" });
  legend.onAdd = () => {
    const div = L.DomUtil.create("div", "map-legend immo-legend");
    div.innerHTML = `<div class="legend-title"></div><ul></ul><div class="legend-hint"></div>`;
    L.DomEvent.disableClickPropagation(div);
    L.DomEvent.disableScrollPropagation(div);
    div.hidden = true;
    return div;
  };
  return legend;
}

function updateImmoLegend() {
  const div = $(".immo-legend");
  const hasData = immoLayer.getLayers().length > 0;
  div.hidden = !hasData || !map.hasLayer(immoLayer);
  if (div.hidden) return;
  const y0 = immoData.date_min ? immoData.date_min.slice(0, 4) : "";
  const y1 = immoData.date_max ? immoData.date_max.slice(0, 4) : "";
  const periode = y0 ? ` (${y0 === y1 ? y0 : `${y0}–${y1}`})` : "";
  div.querySelector(".legend-title").textContent = `Prix au m²${periode}`;
  const rows = [];
  if (immoScale) {
    const { breaks, colors, min, max } = immoScale;
    if (!breaks.length) {
      rows.push([colors[0], min === max ? fmtPrix(min) : `${min.toLocaleString("fr-FR")} – ${fmtPrix(max)}`]);
    } else {
      breaks.forEach((b, i) => {
        const label = i === 0 ? `< ${fmtPrix(b)}` : `${breaks[i - 1].toLocaleString("fr-FR")} – ${fmtPrix(b)}`;
        rows.push([colors[i], label]);
      });
      rows.push([colors[breaks.length], `≥ ${fmtPrix(breaks[breaks.length - 1])}`]);
    }
  }
  // Du plus cher (en haut) au moins cher, comme la légende sécurité.
  div.querySelector("ul").innerHTML =
    rows
      .reverse()
      .map(([c, l]) => `<li><span class="swatch" style="background:${c}"></span>${l}</li>`)
      .join("") + `<li><span class="swatch nodata"></span>Pas de données</li>`;
  div.querySelector(".legend-hint").textContent =
    immoLevel === "iris"
      ? "Niveau : quartiers IRIS — dézoomez pour la médiane par commune"
      : "Niveau : communes — zoomez pour le détail par quartier";
}

// Recalcule l'échelle (zone visible) puis recolore.
function restyleImmobilier() {
  if (!immoLayer) return;
  immoScale = computeImmoScale();
  immoLayer.setStyle(immoStyle);
  updateImmoLegend();
}

// Reconstruit les entités du niveau adapté au zoom courant.
function buildImmobilier() {
  immoLayer.clearLayers();
  immoLevel = immoLevelForZoom();
  if (immoData) {
    const noms = new Map(immoCommunes.map((c) => [c.code_commune, c.nom]));
    let features;
    if (immoLevel === "iris") {
      features = (immoData.quartiers || [])
        .filter((q) => q.contour)
        .map((q) => ({
          type: "Feature",
          geometry: q.contour,
          properties: {
            niveau: "iris",
            nom: q.nom,
            commune: noms.get(q.code_commune),
            prix: q.prix,
          },
        }));
    } else {
      const prix = new Map((immoData.communes || []).map((c) => [c.code_commune, c]));
      features = immoCommunes
        .filter((c) => c.contour)
        .map((c) => {
          const p = prix.get(c.code_commune) || {};
          return {
            type: "Feature",
            geometry: c.contour,
            properties: { niveau: "commune", nom: c.nom, prix: p.prix },
          };
        });
    }
    immoLayer.addData(features);
  }
  restyleImmobilier();
}

function renderImmobilier(immo, communes) {
  immoData = immo || null;
  immoCommunes = communes || [];
  buildImmobilier();
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
  if (communeLayer) renderCommunes([]);
  if (immoLayer) renderImmobilier(null, []);
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

// ---------------------------------------------------------------------------
// Autocomplétion de l'adresse de départ
// ---------------------------------------------------------------------------
(function setupAddressAutocomplete() {
  const input = $("#address");
  const list = $("#address-list");
  const MIN_CHARS = 3;
  const cache = new Map(); // requête (minuscules) → suggestions
  let items = []; // suggestions affichées
  let active = -1; // index de l'élément surligné
  let typed = ""; // texte réellement saisi (sans la complétion en ligne)
  let timer = null;
  let reqSeq = 0; // pour ignorer les réponses obsolètes

  function escapeHTML(str) {
    return str.replace(/[&<>"']/g, (c) => (
      { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
    ));
  }

  function close() {
    list.hidden = true;
    list.innerHTML = "";
    items = [];
    active = -1;
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
  }

  function highlight(i) {
    active = i;
    list.querySelectorAll("li").forEach((li, idx) => {
      li.classList.toggle("active", idx === i);
      li.setAttribute("aria-selected", idx === i ? "true" : "false");
    });
    if (i >= 0) {
      const li = list.children[i];
      input.setAttribute("aria-activedescendant", li.id);
      li.scrollIntoView({ block: "nearest" });
    } else {
      input.removeAttribute("aria-activedescendant");
    }
  }

  function pick(i) {
    const s = items[i];
    if (!s) return;
    input.value = s.label;
    typed = s.label;
    close();
    const end = input.value.length;
    input.setSelectionRange(end, end);
  }

  function show(suggestions) {
    items = suggestions;
    active = -1;
    if (!items.length) {
      close();
      return;
    }
    const q = typed.trim().toLowerCase();
    list.innerHTML = items
      .map((s, i) => {
        const label = s.label;
        // Met en évidence le préfixe saisi lorsqu'il correspond.
        const html = label.toLowerCase().startsWith(q)
          ? "<mark>" + escapeHTML(label.slice(0, q.length)) + "</mark>" + escapeHTML(label.slice(q.length))
          : escapeHTML(label);
        return `<li id="address-opt-${i}" role="option" aria-selected="false">${html}</li>`;
      })
      .join("");
    list.hidden = false;
    input.setAttribute("aria-expanded", "true");
  }

  // Complète en ligne avec la première suggestion si elle prolonge la saisie ;
  // la partie ajoutée est sélectionnée pour être écrasée par la frappe suivante.
  function inlineComplete() {
    const first = items[0];
    if (!first || !typed) return;
    if (input.value !== typed || input.selectionEnd !== typed.length) return;
    if (!first.label.toLowerCase().startsWith(typed.toLowerCase())) return;
    if (first.label.length === typed.length) return;
    input.value = typed + first.label.slice(typed.length);
    input.setSelectionRange(typed.length, input.value.length);
  }

  async function fetchSuggestions(allowInline) {
    const q = typed.trim();
    if (q.length < MIN_CHARS) {
      close();
      return;
    }
    const key = q.toLowerCase();
    const seq = ++reqSeq;
    let suggestions = cache.get(key);
    if (!suggestions) {
      try {
        suggestions = await api("/address/suggest?q=" + encodeURIComponent(q));
        if (!Array.isArray(suggestions)) suggestions = [];
        cache.set(key, suggestions);
      } catch {
        return; // l'autocomplétion est un confort : on ignore les erreurs
      }
    }
    if (seq !== reqSeq || document.activeElement !== input) return;
    show(suggestions);
    if (allowInline) inlineComplete();
  }

  input.addEventListener("input", (e) => {
    typed = input.value;
    const isDelete = (e.inputType || "").startsWith("delete");
    clearTimeout(timer);
    timer = setTimeout(() => fetchSuggestions(!isDelete), 250);
  });

  input.addEventListener("keydown", (e) => {
    const open = !list.hidden && items.length > 0;
    const hasInline =
      input.selectionStart < input.value.length &&
      input.selectionEnd === input.value.length &&
      input.value !== typed;

    switch (e.key) {
      case "ArrowDown":
        if (!open) return;
        e.preventDefault();
        highlight((active + 1) % items.length);
        break;
      case "ArrowUp":
        if (!open) return;
        e.preventDefault();
        highlight(active <= 0 ? items.length - 1 : active - 1);
        break;
      case "Enter":
        if (open && active >= 0) {
          e.preventDefault();
          pick(active);
        } else {
          close();
        }
        break;
      case "Tab":
        if (open && active >= 0) {
          e.preventDefault();
          pick(active);
        } else if (hasInline) {
          e.preventDefault();
          typed = input.value;
          const end = input.value.length;
          input.setSelectionRange(end, end);
          fetchSuggestions(false);
        }
        break;
      case "ArrowRight":
        if (hasInline) typed = input.value; // accepte la complétion en ligne
        break;
      case "Escape":
        if (hasInline) {
          e.preventDefault();
          input.value = typed;
        }
        if (open) {
          e.preventDefault();
          close();
        }
        break;
    }
  });

  // mousedown (et non click) pour passer avant le blur de l'input.
  list.addEventListener("mousedown", (e) => {
    const li = e.target.closest("li");
    if (!li) return;
    e.preventDefault();
    pick(Array.prototype.indexOf.call(list.children, li));
  });

  input.addEventListener("blur", () => {
    clearTimeout(timer);
    reqSeq++;
    close();
  });

  $("#search-form").addEventListener("submit", close);
})();

function render(data) {
  isoLayer.clearLayers();
  markerLayer.clearLayers();
  renderCommunes(data.communes);
  renderImmobilier(data.immobilier, data.communes);

  // Isochrone : geometry = [[lon, lat], ...] → Leaflet attend [lat, lon].
  const iso = data.isochrone;
  if (iso && Array.isArray(iso.geometry) && iso.geometry.length) {
    const ring = iso.geometry.map(([lon, lat]) => [lat, lon]);
    // Contour sombre sans remplissage : lisible par-dessus la choroplèthe bleue.
    const poly = L.polygon(ring, {
      color: "#0f172a",
      weight: 2.5,
      fill: false,
      interactive: false,
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
