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
let currentUser = null; // profil de l'utilisateur connecté (GET /user)
let pendingEmail = null; // e-mail en attente de vérification (pour le renvoi du lien)
let map, isoLayer, markerLayer, communeLayer, immoLayer, fiscLayer, logementLayer, logementRenderer;
let lastData = null; // dernière réponse de /etablissements (re-filtrage côté client)
let levelLegend = null; // légende des niveaux de temps de la zone

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

  currentUser = user || null;
  renderUser();

  if (!map) initMap();
  // Leaflet a besoin de recalculer sa taille une fois le conteneur visible.
  setTimeout(() => map.invalidateSize(), 0);
}

// ---------------------------------------------------------------------------
// Utilisateur : avatar & paramètres du compte
// ---------------------------------------------------------------------------
function displayName(user) {
  if (!user) return "";
  const full = [user.first_name, user.last_name].filter(Boolean).join(" ").trim();
  return full || user.email || user.login || "";
}

function initials(user) {
  if (!user) return "?";
  const parts = [user.first_name, user.last_name].filter(Boolean).map((p) => p.trim().charAt(0));
  const txt = parts.length ? parts.join("") : (user.email || user.login || "?").trim().charAt(0);
  return (txt || "?").toUpperCase();
}

// SHA-256 hexadécimal (identifiant Gravatar). Indisponible hors contexte
// sécurisé (HTTP non local) : renvoie null.
async function sha256Hex(text) {
  if (!window.crypto || !crypto.subtle) return null;
  const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return Array.from(new Uint8Array(buf), (b) => b.toString(16).padStart(2, "0")).join("");
}

// Affiche dans el : l'avatar_url, sinon le Gravatar de l'e-mail, sinon les
// initiales. Une image qui ne se charge pas retombe sur les initiales.
let avatarSeq = 0;
async function renderAvatar(el, user, avatarURL) {
  const seq = (el._avatarSeq = ++avatarSeq);
  const showInitials = () => {
    if (el._avatarSeq !== seq) return;
    el.replaceChildren(document.createTextNode(initials(user)));
  };

  let src = avatarURL !== undefined ? avatarURL : user && user.avatar_url;
  if (!src && user && user.email) {
    const hash = await sha256Hex(user.email.trim().toLowerCase());
    if (hash) src = `https://gravatar.com/avatar/${hash}?s=96&d=404`;
  }
  if (el._avatarSeq !== seq) return; // un rendu plus récent a été demandé
  if (!src) return showInitials();

  const img = new Image();
  img.alt = "";
  img.referrerPolicy = "no-referrer";
  img.onerror = showInitials;
  img.src = src;
  el.replaceChildren(img);
}

function renderUser() {
  $("#user-label").textContent = displayName(currentUser);
  renderAvatar($("#user-avatar"), currentUser);
  $("#admin-btn").hidden = !(currentUser && currentUser.perms === "admin");
}

async function refreshUser() {
  currentUser = await api("/user");
  renderUser();
  fillSettings();
}

function settingsMsg(form, message, isError) {
  const p = form.querySelector(".settings-msg");
  p.textContent = message;
  p.classList.toggle("error", !!isError);
  p.classList.toggle("info", !isError);
  p.hidden = !message;
}

function fillSettings() {
  const u = currentUser || {};
  $("#set-first-name").value = u.first_name || "";
  $("#set-last-name").value = u.last_name || "";
  $("#set-avatar-url").value = u.avatar_url || "";
  renderAvatar($("#profile-avatar"), u);

  $("#current-email").textContent = u.email || "—";
  $("#password-username").value = u.email || "";
  const pending = $("#pending-email");
  pending.hidden = !u.pending_email;
  pending.textContent = u.pending_email
    ? `En attente de confirmation : ${u.pending_email} (consultez cette boîte de réception).`
    : "";

  const hasPassword = !!u.has_password;
  document.querySelectorAll("#settings [data-needs-password]").forEach((el) => {
    el.hidden = !hasPassword;
    el.querySelector("input").required = hasPassword;
  });
  $("#no-password-hint").hidden = hasPassword;
}

$("#user-chip").addEventListener("click", () => {
  document.querySelectorAll("#settings form").forEach((f) => {
    settingsMsg(f, "");
    f.querySelectorAll('input[type="password"], #set-email').forEach((i) => (i.value = ""));
  });
  fillSettings();
  $("#settings").showModal();
});
$("#settings-close").addEventListener("click", () => $("#settings").close());
// Clic sur le fond (hors du panneau) : fermeture.
$("#settings").addEventListener("click", (e) => {
  if (e.target === e.currentTarget) e.currentTarget.close();
});

// Aperçu de l'avatar pendant la saisie de l'URL.
$("#set-avatar-url").addEventListener("input", (e) => {
  const draft = Object.assign({}, currentUser, {
    first_name: $("#set-first-name").value.trim(),
    last_name: $("#set-last-name").value.trim(),
  });
  renderAvatar($("#profile-avatar"), draft, e.target.value.trim() || null);
});

// Soumission d'un formulaire de paramètres : bouton désactivé pendant l'appel,
// message de succès ou d'erreur dans le formulaire.
function settingsSubmit(form, handler) {
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const btn = form.querySelector('button[type="submit"]');
    btn.disabled = true;
    settingsMsg(form, "");
    try {
      settingsMsg(form, await handler());
    } catch (ex) {
      const msg =
        ex.code === "invalid_password" ? "Mot de passe actuel incorrect." :
        ex.status === 409 ? "Cette adresse e-mail est déjà utilisée." :
        ex.message;
      settingsMsg(form, msg, true);
    } finally {
      btn.disabled = false;
    }
  });
}

settingsSubmit($("#profile-form"), async () => {
  const user = await api("/user", {
    method: "PATCH",
    body: JSON.stringify({
      first_name: $("#set-first-name").value.trim() || null,
      last_name: $("#set-last-name").value.trim() || null,
      avatar_url: $("#set-avatar-url").value.trim() || null,
    }),
  });
  currentUser = Object.assign({}, currentUser, user);
  renderUser();
  return "Profil enregistré.";
});

settingsSubmit($("#email-form"), async () => {
  const email = $("#set-email").value.trim();
  await api("/user/email", {
    method: "POST",
    body: JSON.stringify({ email, current_password: $("#email-password").value }),
  });
  $("#set-email").value = "";
  $("#email-password").value = "";
  await refreshUser();
  return (
    `Un lien de confirmation a été envoyé à ${email}. ` +
    "Votre adresse actuelle reste active jusqu'à confirmation."
  );
});

settingsSubmit($("#password-form"), async () => {
  const next = $("#pw-new").value;
  if (next !== $("#pw-confirm").value) throw new Error("Les deux mots de passe ne correspondent pas.");
  await api("/user/password", {
    method: "POST",
    body: JSON.stringify({ current_password: $("#pw-current").value, new_password: next }),
  });
  ["#pw-current", "#pw-new", "#pw-confirm"].forEach((sel) => ($(sel).value = ""));
  const hadPassword = currentUser && currentUser.has_password;
  await refreshUser();
  return hadPassword ? "Mot de passe mis à jour." : "Mot de passe défini.";
});

// ---------------------------------------------------------------------------
// Administration : gestion des utilisateurs (comptes 'admin')
// ---------------------------------------------------------------------------
const PERMS_LABELS = {
  unverified: "Non vérifié",
  verified: "En attente",
  granted: "Autorisé",
  admin: "Administrateur",
};
const PERMS_ORDER = ["unverified", "verified", "granted", "admin"];
let adminUsers = []; // dernière liste de GET /admin/users

function adminMsg(message, isError) {
  const p = $("#admin-msg");
  p.textContent = message;
  p.classList.toggle("error", !!isError);
  p.classList.toggle("info", !isError);
  p.hidden = !message;
}

async function loadAdminUsers() {
  adminMsg("");
  $("#admin-count").textContent = "Chargement…";
  $("#admin-users-list").replaceChildren();
  try {
    adminUsers = await api("/admin/users");
    renderAdminUsers();
  } catch (ex) {
    $("#admin-count").textContent = "";
    adminMsg(ex.message, true);
  }
}

function renderAdminUsers() {
  const q = $("#admin-search").value.trim().toLowerCase();
  const pendingOnly = $("#admin-pending-only").checked;
  const pendingCount = adminUsers.filter((u) => u.perms === "verified").length;
  const shown = adminUsers
    .filter((u) => !pendingOnly || u.perms === "verified")
    .filter((u) => !q || `${displayName(u)} ${u.email || ""}`.toLowerCase().includes(q))
    // Comptes en attente de validation d'abord, puis par e-mail.
    .sort((a, b) =>
      (b.perms === "verified") - (a.perms === "verified") ||
      (a.email || "").localeCompare(b.email || "")
    );

  $("#admin-count").textContent =
    `${adminUsers.length} compte${adminUsers.length > 1 ? "s" : ""}` +
    (pendingCount ? `, dont ${pendingCount} en attente de validation` : "") +
    (shown.length !== adminUsers.length ? ` — ${shown.length} affiché${shown.length > 1 ? "s" : ""}` : "");
  $("#admin-users-list").replaceChildren(...shown.map(adminUserRow));
}

function adminUserRow(u) {
  const isSelf = currentUser && u.id === currentUser.id;
  const li = document.createElement("li");
  li.className = "admin-user";
  li.classList.toggle("pending", u.perms === "verified");

  const avatar = document.createElement("span");
  avatar.className = "avatar";
  avatar.setAttribute("aria-hidden", "true");
  renderAvatar(avatar, u);

  const who = document.createElement("div");
  who.className = "admin-user-who";
  const name = document.createElement("strong");
  name.textContent = displayName(u) + (isSelf ? " (vous)" : "");
  const email = document.createElement("span");
  email.className = "admin-user-email";
  email.textContent = u.email || "—";
  who.append(name, email);
  if (u.email && displayName(u) === u.email) email.hidden = true;

  const select = document.createElement("select");
  select.setAttribute("aria-label", `Niveau d'accès de ${u.email || displayName(u)}`);
  for (const p of PERMS_ORDER) select.add(new Option(PERMS_LABELS[p], p, false, p === u.perms));
  select.disabled = isSelf;
  if (isSelf) select.title = "Vous ne pouvez pas changer votre propre niveau.";
  select.addEventListener("change", async () => {
    const previous = u.perms;
    select.disabled = true;
    adminMsg("");
    try {
      const updated = await api(`/admin/users/${encodeURIComponent(u.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ perms: select.value }),
      });
      Object.assign(u, updated);
      const approved =
        PERMS_ORDER.indexOf(previous) < PERMS_ORDER.indexOf("granted") &&
        PERMS_ORDER.indexOf(u.perms) >= PERMS_ORDER.indexOf("granted");
      adminMsg(
        `${u.email || displayName(u)} : ${PERMS_LABELS[previous]} → ${PERMS_LABELS[u.perms]}.` +
          (approved ? " Un e-mail de validation lui a été envoyé." : "")
      );
      renderAdminUsers();
    } catch (ex) {
      select.value = previous;
      select.disabled = false;
      adminMsg(ex.message, true);
    }
  });

  const del = document.createElement("button");
  del.type = "button";
  del.className = "btn-ghost btn-danger";
  del.textContent = "Supprimer";
  del.hidden = isSelf;
  del.addEventListener("click", async () => {
    const label = u.email || displayName(u);
    if (!confirm(`Supprimer définitivement le compte ${label} ?`)) return;
    del.disabled = true;
    adminMsg("");
    try {
      await api(`/admin/users/${encodeURIComponent(u.id)}`, { method: "DELETE" });
      adminUsers = adminUsers.filter((x) => x.id !== u.id);
      adminMsg(`Compte ${label} supprimé.`);
      renderAdminUsers();
    } catch (ex) {
      del.disabled = false;
      adminMsg(ex.message, true);
    }
  });

  li.append(avatar, who, select, del);
  return li;
}

$("#admin-btn").addEventListener("click", () => {
  $("#admin-users").showModal();
  loadAdminUsers();
});
$("#admin-users-close").addEventListener("click", () => $("#admin-users").close());
$("#admin-users").addEventListener("click", (e) => {
  if (e.target === e.currentTarget) e.currentTarget.close();
});
$("#admin-search").addEventListener("input", renderAdminUsers);
$("#admin-pending-only").addEventListener("change", renderAdminUsers);

function initMap() {
  map = L.map("map", { zoomControl: false }).setView([46.6, 2.5], 6); // France
  L.control.zoom({ position: "topright" }).addTo(map);
  // La taille de la carte suit la grille (points de rupture, rotation) et pas
  // seulement la fenêtre : on prévient Leaflet à chaque redimensionnement.
  let resizeFrame = 0;
  new ResizeObserver(() => {
    cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(() => map.invalidateSize());
  }).observe($(".map-wrap"));
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
  // Fiscalité : même niveau, exclusive elle aussi.
  map.createPane("fiscalite").style.zIndex = 345;
  fiscLayer = L.geoJSON(null, {
    pane: "fiscalite",
    style: fiscStyle,
    onEachFeature: (f, layer) => layer.bindTooltip(fiscTooltip(f.properties), { sticky: true }),
  }); // désactivée par défaut
  // Logements sociaux : au-dessus des choroplèthes, sous les marqueurs.
  // Rendu canvas : une zone peut contenir des milliers de bâtiments.
  map.createPane("logements").style.zIndex = 450;
  logementRenderer = L.canvas({ pane: "logements" });
  logementLayer = L.layerGroup(); // désactivée par défaut
  isoLayer = L.layerGroup().addTo(map);
  markerLayer = L.layerGroup().addTo(map);
  const layers = L.control.layers(
    null,
    {
      [securityLayerLabel()]: communeLayer,
      [immoLayerLabel()]: immoLayer,
      [fiscLayerLabel()]: fiscLayer,
      "Logements sociaux": logementLayer,
    },
    { collapsed: false },
  );
  layers.addTo(map);
  // Le contrôle des couches vit dans le panneau latéral, pas sur la carte.
  $("#layers-slot").appendChild(layers.getContainer());
  // L'icône d'info est dans le <label> de la case : son clic ne doit pas
  // basculer la couche (le survol / focus affiche l'infobulle).
  layers.getContainer().querySelectorAll(".info-tip").forEach((tip) =>
    tip.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
    }),
  );
  securityLegend().addTo(map);
  levelLegend = levelLegendControl().addTo(map);
  immoLegend().addTo(map);
  fiscLegend().addTo(map);
  map.on("overlayadd overlayremove", (e) => {
    const added = e.type === "overlayadd";
    if (e.layer === communeLayer) $(".security-legend").hidden = !added;
    if (e.layer === immoLayer) {
      if (added) restyleImmobilier();
      else $(".immo-legend").hidden = true;
    }
    if (e.layer === fiscLayer) {
      if (added) restyleFiscalite();
      else $(".fisc-legend").hidden = true;
    }
    // Les choroplèthes se superposent mal : en activer une masque les autres.
    // Différé : pendant le clic, le contrôle des couches ne se met pas à jour.
    const choros = [communeLayer, immoLayer, fiscLayer];
    if (added && choros.includes(e.layer)) {
      setTimeout(() => choros.forEach((l) => l !== e.layer && map.removeLayer(l)), 0);
    }
  });
  // Niveau de détail : communes de loin, quartiers IRIS de près.
  map.on("zoomend", () => {
    if (immoData && immoLevelForZoom() !== immoLevel) buildImmobilier();
  });
  // Échelle dynamique : recalculée sur la zone visible.
  map.on("moveend", () => {
    if (immoData && map.hasLayer(immoLayer)) restyleImmobilier();
    if (map.hasLayer(fiscLayer)) restyleFiscalite();
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

// ---------------------------------------------------------------------------
// Niveaux de temps de trajet de la zone de recherche
// ---------------------------------------------------------------------------
const ZONE_COLOR = "#eab308"; // jaune : remplissage des niveaux
const ZONE_STROKE = "#ca8a04"; // contour, plus foncé pour rester lisible
const ZONE_RGB = "234,179,8"; // ZONE_COLOR en RGB (légende)
const ZONE_ALPHA = 0.45; // opacité cumulée du niveau le plus intérieur

// « ≤ N min » pour un niveau exprimé en secondes.
function levelLabel(dist) {
  return `≤ ${Math.round(dist / 60)} min`;
}

function levelLegendControl() {
  const legend = L.control({ position: "bottomleft" });
  legend.onAdd = () => {
    const div = L.DomUtil.create("div", "map-legend level-legend");
    div.hidden = true;
    return div;
  };
  return legend;
}

// Met à jour la légende : le niveau i (par temps croissant) est recouvert par
// les bandes de tous les niveaux >= i, d'où son opacité cumulée.
function updateLevelLegend(levels, multi) {
  if (!levelLegend) return;
  const div = levelLegend.getContainer();
  if (!levels || !levels.length) {
    div.hidden = true;
    div.innerHTML = "";
    return;
  }
  const alpha = ZONE_ALPHA / levels.length;
  const rows = levels
    .map((lv, i) => {
      const a = (alpha * (levels.length - i)).toFixed(3);
      return `<li><span class="swatch" style="background:linear-gradient(rgba(${ZONE_RGB},${a}),rgba(${ZONE_RGB},${a})),#f1f5f9"></span>${levelLabel(lv.dist)}</li>`;
    })
    .join("");
  div.innerHTML =
    `<div class="legend-title">Temps de trajet${multi ? "<br/><small>depuis tous les départs</small>" : ""}</div>` +
    `<ul>${rows}</ul>`;
  div.hidden = false;
}

// Ligne de popup du niveau de temps d'un résultat.
function levelPopupLine(niveau) {
  if (niveau == null) return "";
  const multi = lastData && (lastData.isochrones || []).length > 1;
  return `<div class="popup-meta">Temps de trajet : <strong>${levelLabel(niveau)}</strong>` +
    `${multi ? " depuis tous les départs" : ""}</div>`;
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

// Quantiles (au plus ramp.length classes, bornes arrondies au multiple de
// step) des valeurs (valueOf) des entités visibles de layer, ou de toutes si
// aucune n'est visible.
function computeQuantileScale(layer, valueOf, ramp, step) {
  const view = map.getBounds();
  const all = [];
  let visible = [];
  layer.eachLayer((l) => {
    const v = valueOf(l.feature.properties);
    if (v == null) return;
    all.push(v);
    if (view.intersects(l.getBounds())) visible.push(v);
  });
  const vals = (visible.length ? visible : all).sort((a, b) => a - b);
  if (!vals.length) return null;
  const n = ramp.length;
  const breaks = [];
  for (let i = 1; i < n; i++) {
    const b = Math.round(vals[Math.floor((i * vals.length) / n)] / step) * step;
    if (b > vals[0] && b <= vals[vals.length - 1] && b > (breaks[breaks.length - 1] ?? -Infinity)) {
      breaks.push(b);
    }
  }
  // Couleurs réparties sur toute la rampe quel que soit le nombre de classes.
  const k = breaks.length + 1;
  const colors = Array.from({ length: k }, (_, i) =>
    ramp[k === 1 ? Math.floor(n / 2) : Math.round((i * (n - 1)) / (k - 1))],
  );
  return { breaks, colors, min: vals[0], max: vals[vals.length - 1] };
}

function scaleColor(scale, v) {
  if (v == null || !scale) return NO_DATA_COLOR;
  return scale.colors[scale.breaks.filter((b) => v >= b).length];
}

// Lignes de légende d'une échelle de quantiles, de la classe la plus haute
// (en haut) à la plus basse, comme la légende sécurité. fmt formate une
// valeur avec son unité, num sans.
function scaleLegendRows(scale, fmt, num) {
  const rows = [];
  if (scale) {
    const { breaks, colors, min, max } = scale;
    if (!breaks.length) {
      rows.push([colors[0], min === max ? fmt(min) : `${num(min)} – ${fmt(max)}`]);
    } else {
      breaks.forEach((b, i) => {
        const label = i === 0 ? `< ${fmt(b)}` : `${num(breaks[i - 1])} – ${fmt(b)}`;
        rows.push([colors[i], label]);
      });
      rows.push([colors[breaks.length], `≥ ${fmt(breaks[breaks.length - 1])}`]);
    }
  }
  return (
    rows
      .reverse()
      .map(([c, l]) => `<li><span class="swatch" style="background:${c}"></span>${l}</li>`)
      .join("") + `<li><span class="swatch nodata"></span>Pas de données</li>`
  );
}

function computeImmoScale() {
  return computeQuantileScale(immoLayer, prixFor, IMMO_COLORS, 100);
}

function immoColor(v) {
  return scaleColor(immoScale, v);
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
  div.querySelector("ul").innerHTML = scaleLegendRows(immoScale, fmtPrix, (n) => n.toLocaleString("fr-FR"));
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

// ---------------------------------------------------------------------------
// Choroplèthe « fiscalité » (taux global TFB + taux plein TEOM, dernier exercice)
// ---------------------------------------------------------------------------
// Rampe séquentielle violette, du moins taxé (clair) au plus taxé (foncé).
const FISC_COLORS = ["#efedf5", "#cbc9e2", "#9e9ac8", "#756bb1", "#54278f"];
let fiscScale = null; // échelle courante (voir computeQuantileScale)

const fmtNumTaux = (n) => n.toLocaleString("fr-FR", { maximumFractionDigits: 2 });
const fmtTaux = (n) => `${fmtNumTaux(n)} %`;

function fiscLayerLabel() {
  return (
    `Fiscalité locale ` +
    `<span class="info-tip" tabindex="0" role="button" aria-label="Calcul du taux">i` +
    `<span class="tip-body" role="tooltip">` +
    `<strong>Taxe foncière + ordures ménagères</strong>` +
    `<p>Somme du taux global de taxe foncière sur les propriétés bâties (commune, ` +
    `intercommunalité…) et du taux plein de TEOM, en % de la valeur locative ` +
    `cadastrale (DGFiP, dernier exercice).</p>` +
    `<p>Une commune sans TEOM (ordures financées autrement, p. ex. par la REOM) ` +
    `compte 0 pour celle-ci.</p>` +
    `<p>Les couleurs sont réparties en quantiles des taux de la zone visible.</p>` +
    `</span></span>`
  );
}

const fiscFor = (p) => (p.fisc ? p.fisc.taux : null);

function fiscStyle(feature) {
  const v = fiscFor(feature.properties);
  return {
    color: "#ffffff",
    weight: 1,
    opacity: 0.9,
    fillColor: scaleColor(fiscScale, v),
    fillOpacity: v == null ? 0.35 : 0.65,
    dashArray: v == null ? "3 3" : null,
  };
}

function fiscTooltip(p) {
  const nom = `<strong>${esc(p.nom)}</strong>`;
  const f = p.fisc;
  if (!f) return `${nom}<br>Fiscalité : <span class="muted">pas de données</span>`;
  const teom = f.taux_teom == null ? "pas de TEOM" : fmtTaux(f.taux_teom);
  return (
    `${nom}<br>TFB + TEOM : <strong>${fmtTaux(f.taux)}</strong>` +
    `<br><span class="muted">TFB ${fmtTaux(f.taux_tfb)}, TEOM ${teom}` +
    `<br>exercice ${f.annee}</span>`
  );
}

function fiscLegend() {
  const legend = L.control({ position: "bottomright" });
  legend.onAdd = () => {
    const div = L.DomUtil.create("div", "map-legend fisc-legend");
    div.innerHTML = `<div class="legend-title"></div><ul></ul>`;
    div.hidden = true;
    return div;
  };
  return legend;
}

function updateFiscLegend() {
  const div = $(".fisc-legend");
  div.hidden = fiscLayer.getLayers().length === 0 || !map.hasLayer(fiscLayer);
  if (div.hidden) return;
  let annee = null;
  fiscLayer.eachLayer((l) => {
    const f = l.feature.properties.fisc;
    if (f && (annee == null || f.annee > annee)) annee = f.annee;
  });
  div.querySelector(".legend-title").textContent = `TFB + TEOM${annee ? ` (${annee})` : ""}`;
  div.querySelector("ul").innerHTML = scaleLegendRows(fiscScale, fmtTaux, fmtNumTaux);
}

// Recalcule l'échelle (zone visible) puis recolore.
function restyleFiscalite() {
  if (!fiscLayer) return;
  fiscScale = computeQuantileScale(fiscLayer, fiscFor, FISC_COLORS, 0.5);
  fiscLayer.setStyle(fiscStyle);
  updateFiscLegend();
}

function renderFiscalite(communes) {
  fiscLayer.clearLayers();
  fiscLayer.addData(
    (communes || [])
      .filter((c) => c.contour)
      .map((c) => ({
        type: "Feature",
        geometry: c.contour,
        properties: { nom: c.nom, fisc: c.fiscalite },
      })),
  );
  restyleFiscalite();
}

// ---------------------------------------------------------------------------
// Points de départ : la zone de recherche est l'intersection de leurs
// isochrones. Le premier utilise l'adresse de la barre de recherche.
// ---------------------------------------------------------------------------
const ORIGIN_COLORS = ["#a855f7", "#f97316", "#14b8a6", "#ec4899", "#84cc16", "#3b82f6"];
const MAX_ORIGINS = 10; // même limite que le serveur

function originColor(i) {
  return ORIGIN_COLORS[i % ORIGIN_COLORS.length];
}

// Renumérote les lignes (titre, couleur) après un ajout ou une suppression.
function refreshOrigins() {
  const rows = document.querySelectorAll("#origins .origin-row");
  rows.forEach((row, i) => {
    row.querySelector(".origin-dot").style.background = originColor(i);
    row.querySelector(".origin-title").textContent = `Départ ${i + 1}`;
    // Au moins un point de départ est requis : pas de suppression s'il est seul.
    row.querySelector(".origin-remove").hidden = rows.length === 1;
  });
  $("#add-origin").hidden = rows.length >= MAX_ORIGINS;
}

function addOrigin({ focus = true } = {}) {
  const row = $("#origin-tpl").content.firstElementChild.cloneNode(true);
  const range = row.querySelector('input[type="range"]');
  range.addEventListener("input", () => {
    row.querySelector(".origin-time-val").textContent = range.value;
  });
  row.querySelector(".origin-remove").addEventListener("click", () => {
    row.remove();
    refreshOrigins();
  });
  const input = row.querySelector(".origin-address input");
  setupAddressAutocomplete(input, row.querySelector(".origin-address .ac-list"));
  $("#origins").appendChild(row);
  refreshOrigins();
  if (focus) input.focus();
}

// Liste des points de départ saisis : [{adresse, dist, transp}, ...].
function collectOrigins() {
  return Array.from(document.querySelectorAll("#origins .origin-row")).map((row) => ({
    adresse: row.querySelector(".origin-address input").value.trim(),
    dist: parseInt(row.querySelector('input[type="range"]').value, 10) * 60, // minutes → secondes
    transp: row.querySelector("select").value,
  }));
}

$("#add-origin").addEventListener("click", () => addOrigin());

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
  if (fiscLayer) renderFiscalite([]);
  if (logementLayer) renderLogements([]);
  updateLevelLegend([]);
  lastData = null;
  renderInsights([]);
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

  const origines = collectOrigins();
  const missing = origines.findIndex((o) => !o.adresse);
  if (missing >= 0) {
    status.textContent = `Adresse manquante pour le départ ${missing + 1}.`;
    status.classList.add("err");
    return;
  }

  status.textContent = "Recherche en cours…";
  btn.disabled = true;

  const body = {
    origines,
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
let acSeq = 0; // préfixe unique des identifiants d'options par liste

function setupAddressAutocomplete(input, list) {
  const optPrefix = `ac${++acSeq}-opt-`;
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
        return `<li id="${optPrefix}${i}" role="option" aria-selected="false">${html}</li>`;
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
}

addOrigin({ focus: false });

function render(data) {
  isoLayer.clearLayers();
  markerLayer.clearLayers();
  renderCommunes(data.communes);
  renderImmobilier(data.immobilier, data.communes);
  renderFiscalite(data.communes);
  renderLogements(data.logements_sociaux || []);

  // Isochrones : geometry = [[lon, lat], ...] → Leaflet attend [lat, lon].
  const isos = Array.isArray(data.isochrones) && data.isochrones.length
    ? data.isochrones
    : data.isochrone ? [data.isochrone] : [];
  const multi = isos.length > 1;
  const zoneBounds = L.latLngBounds([]);
  isos.forEach((iso, i) => {
    // Plusieurs départs : isochrones emboîtées de chaque départ en pointillés
    // à sa couleur (avec un seul départ, elles se confondent avec la zone).
    const levels = iso.levels && iso.levels.length ? iso.levels : [{ dist: iso.dist, geometry: iso.geometry }];
    levels.forEach((lv, k) => {
      if (!Array.isArray(lv.geometry) || !lv.geometry.length) return;
      const outer = k === levels.length - 1;
      const poly = L.polygon(lv.geometry.map(([lon, lat]) => [lat, lon]), {
        color: originColor(i),
        weight: outer ? 2.2 : 1.4,
        opacity: outer ? 1 : 0.75,
        dashArray: "6 5",
        fill: false,
        interactive: false,
      });
      if (multi) isoLayer.addLayer(poly);
      zoneBounds.extend(poly.getBounds());
    });
    if (iso.lonlat) {
      L.circleMarker([iso.lonlat[1], iso.lonlat[0]], {
        radius: 7,
        color: originColor(i),
        fillColor: originColor(i),
        fillOpacity: 0.9,
      })
        .bindPopup(multi ? `Point de départ ${i + 1}` : "Point de départ")
        .addTo(isoLayer);
    }
  });

  // Niveaux de la zone de recherche (intersection des isochrones par temps) :
  // bandes superposées de l'extérieur vers l'intérieur, plus sombres au centre.
  const zoneLevels = data.zone_levels || [];
  const alpha = zoneLevels.length ? ZONE_ALPHA / zoneLevels.length : 0;
  zoneLevels
    .slice()
    .reverse()
    .forEach((lv, k) => {
      const outer = k === 0;
      L.geoJSON(lv.geometry, {
        style: {
          color: ZONE_STROKE,
          weight: outer ? 2.5 : 1,
          opacity: outer ? 1 : 0.55,
          fillColor: ZONE_COLOR,
          fillOpacity: alpha,
        },
        interactive: false,
      }).addTo(isoLayer);
    });
  updateLevelLegend(zoneLevels, multi);

  lastData = data;
  const bounds = renderMarkers();

  if (multi && !data.zone) {
    const status = $("#search-status");
    status.textContent = "Aucune zone commune à tous les points de départ.";
    status.classList.add("err");
  }

  showMapOnNarrow();
  if (bounds.length) {
    map.fitBounds(bounds, { padding: [40, 40], maxZoom: 14 });
  } else if (zoneBounds.isValid()) {
    map.fitBounds(zoneBounds, { padding: [40, 40], maxZoom: 13 });
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
  markerByEtab = new Map();
  for (const etab of etabs) {
    const coords = etab.position && etab.position.coordinates;
    if (!coords) continue;
    const [lon, lat] = coords;
    bounds.push([lat, lon]);
    const marker = L.marker([lat, lon]).bindPopup(popupHtml(etab)).addTo(markerLayer);
    markerByEtab.set(etab, marker);
  }
  renderInsights(etabs);

  const status = $("#search-status");
  status.classList.remove("err");
  status.textContent =
    etabs.length === all.length
      ? `${all.length} établissement(s) trouvé(s).`
      : `${etabs.length} établissement(s) affiché(s) sur ${all.length} trouvé(s).`;
  const nbLogements = (lastData.logements_sociaux || []).reduce((n, p) => n + p.nb_logements, 0);
  if (nbLogements) status.textContent += ` ${nbLogements} logement(s) social(aux).`;
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
  html += levelPopupLine(etab.niveau);

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
// Logements sociaux (RPLS) : un point par position (bâtiment)
// ---------------------------------------------------------------------------
function renderLogements(positions) {
  logementLayer.clearLayers();
  for (const p of positions) {
    // Rayon croissant (log) avec le nombre de logements du bâtiment.
    const radius = Math.min(12, 3 + 1.5 * Math.log2(Math.max(1, p.nb_logements)));
    L.circleMarker([p.lat, p.lon], {
      renderer: logementRenderer,
      pane: "logements",
      radius,
      color: "#fff",
      weight: 1,
      fillColor: "#ea580c",
      fillOpacity: 0.85,
    })
      .bindPopup(() => logementPopupHtml(p))
      .addTo(logementLayer);
  }
}

function logementPopupHtml(p) {
  const dash = (v) => (v == null ? "—" : esc(v));
  let html = `<div class="popup-title">${p.nb_logements} logement(s) social(aux)</div>`;
  html += `<div class="popup-meta">Position : ${p.lat.toFixed(5)}, ${p.lon.toFixed(5)}</div>`;
  html += levelPopupLine(p.niveau);
  html += `<div class="popup-scroll"><table class="popup-table"><thead><tr>
    <th>Type</th><th>Droit</th><th>Pièces</th><th>Surface (m²)</th><th>Année</th><th>Nb</th>
    </tr></thead><tbody>`;
  for (const g of p.logements || []) {
    html += `<tr><td>${dash(g.type_construction)}</td><td>${dash(g.type_droit_immobilier)}</td>
      <td>${dash(g.nb_pieces)}</td><td>${dash(g.surface)}</td>
      <td>${dash(g.annee_construction)}</td><td>${esc(g.nb)}</td></tr>`;
  }
  html += `</tbody></table></div>`;
  return html;
}

// ---------------------------------------------------------------------------
// Panneau de droite : onglets, répartition par nature, taux, liste
// ---------------------------------------------------------------------------
let markerByEtab = new Map();

// Sur téléphone, la carte, les filtres et la liste sont empilés : on ramène
// la carte à l'écran quand une action la met à jour.
const narrowQuery = window.matchMedia("(max-width: 720px)");
function showMapOnNarrow() {
  if (narrowQuery.matches) $(".map-wrap").scrollIntoView({ behavior: "smooth", block: "start" });
}

document.querySelectorAll(".tabs .tab").forEach((tab) =>
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tabs .tab").forEach((t) => {
      const on = t === tab;
      t.classList.toggle("active", on);
      t.setAttribute("aria-selected", on);
    });
    document.querySelectorAll(".tab-pane").forEach((pane) => {
      pane.hidden = pane.dataset.pane !== tab.dataset.tab;
    });
  }),
);

// Couleur attachée à la nature (jamais au rang) : ordre catégoriel fixe.
const NATURE_GROUPS = [
  { key: "matern", label: "Maternelle", color: "#3987e5" },
  { key: "elem", label: "Élémentaire", color: "#d95926" },
  { key: "coll", label: "Collège", color: "#199e70" },
  { key: "lycee", label: "Lycée", color: "#c98500" },
];
const OTHER_GROUP = { key: "", label: "Autres", color: "#6b7280" };

function natureGroup(nature) {
  const n = String(nature || "").normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase();
  return NATURE_GROUPS.find((g) => n.includes(g.key)) || OTHER_GROUP;
}

const EMPTY_HINT = `<p class="empty">Lancez une recherche pour afficher les statistiques.</p>`;

function renderInsights(etabs) {
  renderNatureChart(etabs);
  renderRateChart(etabs);
  const rated = etabs
    .map((e) => ({ e, r: successRate(e) }))
    .filter((x) => x.r != null)
    .sort((a, b) => b.r - a.r)
    .slice(0, 5);
  renderEtabList($("#etab-top"), rated.map((x) => x.e), true);
  renderEtabList($("#etab-list"), etabs, false);
}

function renderNatureChart(etabs) {
  const el = $("#nature-chart");
  if (!etabs.length) {
    el.innerHTML = EMPTY_HINT;
    return;
  }
  const counts = new Map();
  for (const e of etabs) {
    const g = natureGroup(e.nature);
    counts.set(g, (counts.get(g) || 0) + 1);
  }
  const groups = [...NATURE_GROUPS, OTHER_GROUP].filter((g) => counts.has(g));
  const total = etabs.length;
  const R = 52;
  const C = 2 * Math.PI * R;
  const gap = groups.length > 1 ? 3 : 0; // espace (surface) entre segments
  let offset = 0;
  const arcs = groups
    .map((g) => {
      const n = counts.get(g);
      const len = (n / total) * C;
      const arc =
        `<circle cx="64" cy="64" r="${R}" fill="none" stroke="${g.color}" stroke-width="12"` +
        ` stroke-dasharray="${Math.max(0, len - gap)} ${C}" stroke-dashoffset="${-offset}"` +
        ` transform="rotate(-90 64 64)"><title>${g.label} : ${n}</title></circle>`;
      offset += len;
      return arc;
    })
    .join("");
  const legend = groups
    .map((g) => {
      const n = counts.get(g);
      return (
        `<li><span class="dot" style="background:${g.color}"></span>` +
        `<span class="lg-name">${g.label}</span>` +
        `<span class="lg-val">${Math.round((100 * n) / total)} %</span></li>`
      );
    })
    .join("");
  el.innerHTML =
    `<div class="donut-wrap">` +
    `<svg class="donut" viewBox="0 0 128 128" role="img" aria-label="Répartition des établissements par nature">` +
    `${arcs}<text x="64" y="62" class="donut-num">${total}</text>` +
    `<text x="64" y="80" class="donut-sub">établ.</text></svg>` +
    `<ul class="chart-legend">${legend}</ul></div>`;
}

function renderRateChart(etabs) {
  const el = $("#rate-chart");
  if (!etabs.length) {
    el.innerHTML = EMPTY_HINT;
    return;
  }
  const bins = new Array(10).fill(0);
  let none = 0;
  for (const e of etabs) {
    const r = successRate(e);
    if (r == null) none++;
    else bins[Math.min(9, Math.floor(r / 10))]++;
  }
  const max = Math.max(...bins);
  if (!max) {
    el.innerHTML = `<p class="empty">Aucun résultat d'examen pour ces établissements.</p>`;
    return;
  }
  const bars = bins
    .map((n, i) => {
      const h = n ? Math.max(2, (100 * n) / max) : 0;
      return (
        `<div class="bar-slot" title="${i * 10}–${i * 10 + 10} % : ${n} établissement(s)">` +
        `<div class="bar" style="height:${h}%"></div></div>`
      );
    })
    .join("");
  el.innerHTML =
    `<div class="bars-head"><span>Établissements par tranche de taux</span><span>max ${max}</span></div>` +
    `<div class="bars">${bars}</div>` +
    `<div class="bars-axis"><span>0 %</span><span>50 %</span><span>100 %</span></div>` +
    (none ? `<p class="chart-note">${none} sans résultats d'examen</p>` : "");
}

function secteurClass(secteur) {
  const s = String(secteur || "").toLowerCase();
  if (s.includes("hors")) return "badge-hc";
  if (s.includes("priv")) return "badge-prive";
  return "badge-public";
}

function renderEtabList(ul, etabs, withRate) {
  if (!etabs.length) {
    ul.innerHTML = `<li class="empty">${lastData ? "Aucun établissement." : "Aucune recherche."}</li>`;
    return;
  }
  ul.innerHTML = "";
  for (const e of etabs) {
    const li = document.createElement("li");
    li.className = "etab-item";
    li.tabIndex = 0;
    const rate = successRate(e);
    const sub = [e.nature, e.commune].filter(Boolean).join(" · ");
    li.innerHTML =
      `<div class="etab-main"><div class="etab-name">${esc(e.nom)}</div>` +
      `<div class="etab-sub">${esc(sub)}</div></div>` +
      `<div class="etab-side">` +
      (e.niveau != null ? `<span class="etab-time">${levelLabel(e.niveau)}</span>` : "") +
      (withRate && rate != null ? `<span class="etab-rate">${rate.toFixed(0)} %</span>` : "") +
      (e.secteur ? `<span class="badge ${secteurClass(e.secteur)}">${esc(e.secteur)}</span>` : "") +
      `</div>`;
    const open = () => {
      const m = markerByEtab.get(e);
      if (!m) return;
      showMapOnNarrow();
      map.setView(m.getLatLng(), Math.max(map.getZoom(), 15));
      m.openPopup();
    };
    li.addEventListener("click", open);
    li.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter") open();
    });
    ul.appendChild(li);
  }
}

renderInsights([]);

// ---------------------------------------------------------------------------
// Démarrage : session existante ?
// ---------------------------------------------------------------------------
(async function boot() {
  applyMode();

  let verifyFailed = false;
  let verifiedPending = false;

  // Bouton de connexion OIDC, si le fournisseur est configuré côté serveur.
  fetch("/api/auth/oidc/config")
    .then((res) => (res.ok ? res.json() : null))
    .then((cfg) => {
      if (!cfg || !cfg.enabled) return;
      $("#oidc-login").textContent = `Se connecter avec ${cfg.name || "SSO"}`;
      $("#oidc-block").hidden = false;
    })
    .catch(() => {});

  // Retour du flux OIDC : token dans le fragment (#oidc_token=...) ou erreur
  // dans la requête (?oidc_error=...). L'URL est nettoyée dans les deux cas.
  const oidcToken = new URLSearchParams(location.hash.slice(1)).get("oidc_token");
  const oidcError = new URLSearchParams(location.search).get("oidc_error");
  if (oidcToken || oidcError) history.replaceState(null, "", "/");
  if (oidcToken) setToken(oidcToken);

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
          if (data.email_changed) {
            $("#user-chip").click();
            settingsMsg($("#email-form"), `Adresse e-mail confirmée : ${data.user.email}.`);
          }
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
  if (oidcError === "account_not_granted") {
    showInfo(PENDING_MSG, false);
  } else if (oidcError === "email_taken") {
    showError(
      "Un compte existe déjà avec cette adresse e-mail. Connectez-vous avec votre mot de passe."
    );
  } else if (oidcError) {
    showError("La connexion via le fournisseur d'identité a échoué. Veuillez réessayer.");
  } else if (verifiedPending) {
    showInfo("Adresse e-mail vérifiée, merci ! " + PENDING_MSG, false);
  } else if (verifyFailed) {
    showInfo(
      "Ce lien de vérification est invalide ou a expiré. Connectez-vous pour en recevoir un nouveau.",
      false
    );
  }
})();
