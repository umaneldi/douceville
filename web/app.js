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
let map, isoLayer, markerLayer, communeLayer, immoLayer, fiscLayer, logementLayer, logementRenderer, routeLayer;
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

  currentUser = user || null;
  renderUser();

  if (!$("#criteres").children.length) buildCriteres();
  if (!map) initMap();
  // Leaflet a besoin de recalculer sa taille une fois le conteneur visible.
  setTimeout(() => map.invalidateSize(), 0);
  loadBilling();

  // Premier démarrage : état de la page repris de l'URL (favori, lien partagé)
  // et recherche relancée s'il y a un centre et au moins une nature cochée.
  if (!urlReady) {
    const run = applyQuery(new URLSearchParams(location.search));
    urlReady = true;
    syncUrl();
    if (run) $("#search-form").requestSubmit();
  }
}

// ---------------------------------------------------------------------------
// État de la page dans l'URL : centre, rayon, critères par nature, filtres de
// zone, couches et quartier déplié, pour qu'un favori ou un lien partagé
// restaure la page (et relance la recherche). Ex. :
// /?centre=Toulouse&rayon=10&critere=Lycée,20,voiture,public+privé,85-100
// &critere=Collège,10,pied,public,0-100&prix=0-4000&securite=50&fiscalite=45
// &couche=prix&logements=1&taux=8,9&quartier=315550101
// (taux : tranches de 10 % sélectionnées dans l'histogramme des taux de réussite ;
// quartier : code IRIS du quartier déplié dans l'arbre des quartiers)
// ---------------------------------------------------------------------------
const TRANSP_CODES = { "driving-car": "voiture", "cycling-regular": "velo", "foot-walking": "pied" };
const TRANSP_BY_CODE = Object.fromEntries(Object.entries(TRANSP_CODES).map(([k, v]) => [v, k]));
const RETURN_QUERY_KEY = "douceville_return_query"; // état conservé pendant SSO / paiement
let urlReady = false; // false tant que l'état de l'URL n'a pas été appliqué
// Sélection de l'histogramme des taux lue dans l'URL, appliquée aux résultats
// de la première recherche (null : aucune).
let pendingRateSel = null;
// Quartier (code IRIS) lu dans l'URL, déplié après la première recherche
// (null : aucun).
let pendingQuartier = null;
let openIris = null; // code IRIS du quartier déplié (= sélectionné), null : aucun

function pageQuery() {
  const q = new URLSearchParams();
  const centre = $("#centre").value.trim();
  if (centre) q.set("centre", centre);
  q.set("rayon", $("#rayon").value);
  for (const c of collectCriteres()) {
    q.append(
      "critere",
      [c.nature, c.dist / 60, TRANSP_CODES[c.transp] || c.transp, c.secteur.join("+"), `${c.taux_min}-${c.taux_max}`]
        .concat(c.filiere ? [c.filiere.join("+")] : [])
        .join(","),
    );
  }
  const f = collectFiltres();
  if (f.prix_m2_min != null || f.prix_m2_max != null) q.set("prix", `${f.prix_m2_min ?? ""}-${f.prix_m2_max ?? ""}`);
  if (f.securite_min != null) q.set("securite", f.securite_min);
  if (f.fiscalite_max != null) q.set("fiscalite", f.fiscalite_max);
  if (map) {
    const couche = map.hasLayer(communeLayer) ? "securite"
      : map.hasLayer(immoLayer) ? "prix"
      : map.hasLayer(fiscLayer) ? "fiscalite"
      : "aucune";
    if (couche !== "securite") q.set("couche", couche);
    if (map.hasLayer(logementLayer)) q.set("logements", "1");
  }
  // Sélection de l'histogramme des taux (en attente tant que la recherche
  // relancée depuis l'URL n'a pas répondu).
  const sel = pendingRateSel || rateSel;
  if (sel.size) q.set("taux", [...sel].sort((a, b) => a - b).join(","));
  const iris = pendingQuartier || openIris;
  if (iris) q.set("quartier", iris);
  return q;
}

// Reflète l'état courant dans l'URL (sans entrée d'historique).
function syncUrl() {
  if (!urlReady) return;
  // Virgules et « + » laissés en clair (valides dans une requête) : URL plus lisible.
  const qs = pageQuery().toString().replace(/%2C/gi, ",").replace(/%2B/gi, "+");
  history.replaceState(null, "", qs ? `/?${qs}` : "/");
}

// Applique l'état décrit par params ; renvoie true si une recherche peut être
// lancée (un centre et au moins un critère).
function applyQuery(params) {
  if (params.has("centre")) $("#centre").value = params.get("centre");
  const rayon = parseInt(params.get("rayon"), 10);
  if (Number.isFinite(rayon)) {
    $("#rayon").value = Math.min(50, Math.max(1, rayon));
    $("#rayon-val").textContent = $("#rayon").value;
  }

  // Critères : « nature,minutes,transport,secteurs (+),tmin-tmax[,filières (+)] »
  // (filières absentes : toutes).
  const criteres = params.getAll("critere");
  if (criteres.length) {
    for (const row of document.querySelectorAll("#criteres .critere-row")) setCritere(row, null);
    for (const c of criteres) {
      const [nature, min, transp, secteurs, taux, filieres] = c.split(",");
      const row = document.querySelector(`#criteres .critere-row[data-nature="${CSS.escape(nature || "")}"]`);
      if (!row) continue;
      const t = /^(\d+)-(\d+)$/.exec(taux || "");
      setCritere(row, {
        min: parseInt(min, 10),
        transp: TRANSP_BY_CODE[transp] || transp,
        secteur: secteurs ? secteurs.split(/[+ ]/).filter(Boolean) : null,
        taux: t ? [parseInt(t[1], 10), parseInt(t[2], 10)] : null,
        filiere: filieres ? filieres.split(/[+ ]/).filter(Boolean) : null,
      });
    }
  }

  const prix = /^(\d*)-(\d*)$/.exec(params.get("prix") || "");
  if (prix) {
    $("#prix-min").value = prix[1];
    $("#prix-max").value = prix[2];
  }
  if (params.has("securite")) $("#securite-min").value = params.get("securite");
  if (params.has("fiscalite")) $("#fiscalite-max").value = params.get("fiscalite");

  const couche = params.get("couche");
  const choros = { securite: communeLayer, prix: immoLayer, fiscalite: fiscLayer, aucune: null };
  if (couche in choros) {
    for (const l of [communeLayer, immoLayer, fiscLayer]) if (l !== choros[couche]) map.removeLayer(l);
    if (choros[couche]) map.addLayer(choros[couche]);
  }
  if (params.get("logements") === "1") map.addLayer(logementLayer);

  const taux = (params.get("taux") || "").split(",").filter((x) => /^\d$/.test(x)).map(Number);
  pendingRateSel = taux.length ? new Set(taux) : null;
  pendingQuartier = params.get("quartier") || null;

  updateSearchCost();
  return !!$("#centre").value.trim() && collectCriteres().length > 0;
}

// Avant une redirection qui revient sur « / » (SSO, paiement) : l'état est
// mis de côté, puis restauré au retour (voir boot).
function saveReturnQuery() {
  if (location.search) sessionStorage.setItem(RETURN_QUERY_KEY, location.search);
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
// Crédit prépayé : solde, tarifs, rechargement (Mollie)
// ---------------------------------------------------------------------------
let billing = null; // dernière réponse de GET /billing

function fmtMoney(cents, currency) {
  return new Intl.NumberFormat("fr-FR", {
    style: "currency",
    currency: currency || (billing && billing.currency) || "EUR",
  }).format((cents || 0) / 100);
}

const TX_LABELS = {
  topup: "Rechargement",
  usage: "Requête",
  refund: "Remboursement",
  admin_adjust: "Ajustement",
  welcome: "Bienvenue",
};

async function loadBilling() {
  try {
    billing = await api("/billing");
  } catch (_) {
    billing = null;
  }
  renderBilling();
}

// Solde mis à jour après une requête payante (en-tête X-Credit-Balance) ou
// relu auprès du serveur.
function setBalance(cents) {
  if (!billing) return;
  billing.balance_cents = cents;
  renderBilling();
}

function renderBilling() {
  const on = !!(billing && billing.enabled);
  for (const pill of [$("#user-balance"), $("#rail-balance")]) {
    pill.hidden = !on;
    if (!on) continue;
    pill.textContent = fmtMoney(billing.balance_cents);
    pill.classList.toggle("low", !billing.free && billing.balance_cents < billing.price_per_origin_cents);
    pill.title = billing.free
      ? `Crédit : ${fmtMoney(billing.balance_cents)} (administrateur : requêtes gratuites)`
      : `Crédit disponible : ${fmtMoney(billing.balance_cents)} — cliquer pour recharger`;
    pill.setAttribute("aria-label", pill.title);
  }

  const form = $("#billing-form");
  form.hidden = !on;
  if (on) {
    $("#billing-test-badge").hidden = billing.mode !== "test";
    $("#billing-balance").textContent = billing.free
      ? "illimité (administrateur)"
      : fmtMoney(billing.balance_cents);
    $("#billing-prices").textContent =
      `Tarifs : ${fmtMoney(billing.price_per_origin_cents)} par isochrone calculée lors d'une recherche ` +
      `(isochrones en cache gratuites)` +
      (billing.isochrone_price_cents ? `, ${fmtMoney(billing.isochrone_price_cents)} par isochrone.` : ".");
    const amount = $("#topup-amount");
    amount.min = (billing.min_topup_cents / 100).toFixed(2);
    amount.max = (billing.max_topup_cents / 100).toFixed(2);
    amount.placeholder = `Montant (min. ${fmtMoney(billing.min_topup_cents)})`;
    form.querySelectorAll("[data-amount]").forEach((b) => {
      const c = parseInt(b.dataset.amount, 10);
      b.hidden = c < billing.min_topup_cents || c > billing.max_topup_cents;
    });

    const list = $("#billing-transactions");
    list.replaceChildren(
      ...billing.transactions.map((t) => {
        const li = document.createElement("li");
        const when = document.createElement("span");
        when.className = "tx-date";
        when.textContent = new Date(t.created_at).toLocaleString("fr-FR", { dateStyle: "short", timeStyle: "short" });
        const what = document.createElement("span");
        what.className = "tx-what";
        what.textContent = t.description || TX_LABELS[t.kind] || t.kind;
        const amt = document.createElement("span");
        amt.className = "tx-amount " + (t.amount_cents < 0 ? "neg" : "pos");
        amt.textContent = (t.amount_cents > 0 ? "+" : "") + fmtMoney(t.amount_cents);
        li.append(when, what, amt);
        return li;
      })
    );
    if (!billing.transactions.length) {
      const li = document.createElement("li");
      li.className = "hint";
      li.textContent = "Aucun mouvement.";
      list.append(li);
    }
  }
  updateSearchCost();
}

// Coût estimé de la recherche, affiché sous le bouton « Rechercher ».
function updateSearchCost() {
  const el = $("#search-cost");
  if (!el) return;
  const on = !!(billing && billing.enabled);
  el.hidden = !on;
  if (!on) return;
  if (billing.free) {
    el.textContent = "Recherches gratuites (administrateur).";
    el.classList.remove("err");
    return;
  }
  // Une isochrone par établissement source du rayon (le serveur peut les
  // plafonner) ; celles déjà en cache (recherche récente sur les mêmes
  // établissements) sont gratuites. Le coût exact est estimé au lancement.
  el.textContent =
    `Coût : ${fmtMoney(billing.price_per_origin_cents)} par isochrone calculée (une par établissement ` +
    `source du rayon ; isochrones en cache gratuites), estimé avant le lancement · ` +
    `solde ${fmtMoney(billing.balance_cents)}`;
  el.classList.toggle("err", billing.price_per_origin_cents > billing.balance_cents);
}

// Résumé d'une estimation de recherche : sources, isochrones à calculer et coût.
function estimationText(est) {
  if (est.criteres_vides && est.criteres_vides.length) {
    return `aucun établissement source pour : ${est.criteres_vides.map(natureLabel).join(", ")}`;
  }
  const iso = `${est.a_calculer} isochrone(s) à calculer sur ${est.isochrones}`;
  const cost = est.gratuit ? "gratuit" : fmtMoney(est.cout_cents);
  return `${est.sources} établissement(s) source(s), ${iso} : ${cost}`;
}

// Ouvre les paramètres sur la section « Crédit », avec un message éventuel.
function openBilling(message, isError) {
  if (!$("#settings").open) $("#user-chip").click();
  const form = $("#billing-form");
  settingsMsg(form, message || "", isError);
  form.scrollIntoView({ block: "start" });
  if (!message) $("#topup-amount").focus();
}

$("#user-balance").addEventListener("click", () => openBilling());
$("#rail-balance").addEventListener("click", () => openBilling());

$("#billing-form").querySelectorAll("[data-amount]").forEach((b) =>
  b.addEventListener("click", () => {
    $("#topup-amount").value = (parseInt(b.dataset.amount, 10) / 100).toFixed(2);
  })
);

settingsSubmit($("#billing-form"), async () => {
  const cents = Math.round(parseFloat($("#topup-amount").value.replace(",", ".")) * 100);
  if (!Number.isFinite(cents) || cents <= 0) throw new Error("Montant invalide.");
  const data = await api("/billing/topup", {
    method: "POST",
    body: JSON.stringify({ amount_cents: cents }),
  });
  saveReturnQuery();
  location.assign(data.checkout_url); // page de paiement Mollie
  return "Redirection vers la page de paiement…";
});

// Retour de la page de paiement (/?billing_return=1) : relit les paiements en cours.
async function handleBillingReturn() {
  let msg = "Paiement en cours de traitement : votre solde sera crédité dès sa confirmation.";
  let isError = false;
  try {
    const res = await api("/billing/sync", { method: "POST" });
    const credited = res.payments.filter((p) => p.credited || p.status === "paid");
    const failed = res.payments.filter((p) => ["failed", "canceled", "expired"].includes(p.status));
    if (credited.length) {
      msg = `Paiement confirmé : ${fmtMoney(credited.reduce((a, p) => a + p.amount_cents, 0))} crédité(s). Merci !`;
    } else if (failed.length) {
      msg = "Le paiement n'a pas abouti : aucun montant n'a été débité.";
      isError = true;
    }
  } catch (ex) {
    msg = ex.message;
    isError = true;
  }
  await loadBilling();
  openBilling(msg, isError);
}

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

  const credit = document.createElement("button");
  credit.type = "button";
  credit.className = "btn-ghost admin-credit";
  credit.textContent = fmtMoney(u.balance_cents);
  credit.title = "Ajuster le crédit";
  credit.addEventListener("click", async () => {
    const label = u.email || displayName(u);
    const raw = prompt(`Ajustement du crédit de ${label} (solde ${fmtMoney(u.balance_cents)}).\n` +
      "Montant en euros, négatif pour retirer (ex. 5 ou -2,50) :");
    if (raw === null || !raw.trim()) return;
    const delta = Math.round(parseFloat(raw.replace(",", ".")) * 100);
    if (!Number.isFinite(delta) || delta === 0) return adminMsg("Montant invalide.", true);
    const reason = prompt("Motif (visible dans l'historique de l'utilisateur) :");
    if (!reason || !reason.trim()) return adminMsg("Un motif est requis.", true);
    credit.disabled = true;
    adminMsg("");
    try {
      const res = await api(`/admin/users/${encodeURIComponent(u.id)}/credit`, {
        method: "POST",
        body: JSON.stringify({ delta_cents: delta, reason: reason.trim() }),
      });
      u.balance_cents = res.balance_cents;
      adminMsg(`${label} : ${delta > 0 ? "+" : ""}${fmtMoney(delta)}, nouveau solde ${fmtMoney(u.balance_cents)}.`);
      if (isSelf) loadBilling();
      renderAdminUsers();
    } catch (ex) {
      credit.disabled = false;
      adminMsg(ex.message, true);
    }
  });

  const withCredit = !!(billing && billing.enabled);
  li.classList.toggle("with-credit", withCredit);
  li.append(avatar, who, ...(withCredit ? [credit] : []), select, del);
  return li;
}

$("#admin-btn").addEventListener("click", () => {
  $("#admin-users").showModal();
  loadAdminUsers();
  loadPricing();
});

// Tarifs (administrateurs), saisis en euros et transmis en centimes.
const PRICING_FIELDS = {
  "#price-origin": "price_per_origin_cents",
  "#price-isochrone": "isochrone_price_cents",
  "#price-welcome": "welcome_credit_cents",
  "#price-min-topup": "min_topup_cents",
};

async function loadPricing() {
  const form = $("#admin-pricing-form");
  settingsMsg(form, "");
  form.hidden = !(billing && billing.enabled);
  if (form.hidden) return;
  $("#admin-test-badge").hidden = billing.mode !== "test";
  try {
    const s = await api("/admin/billing/settings");
    for (const [sel, key] of Object.entries(PRICING_FIELDS)) $(sel).value = (s[key] / 100).toFixed(2);
  } catch (ex) {
    settingsMsg(form, ex.message, true);
  }
}

settingsSubmit($("#admin-pricing-form"), async () => {
  const body = {};
  for (const [sel, key] of Object.entries(PRICING_FIELDS)) {
    body[key] = Math.round(parseFloat($(sel).value) * 100);
  }
  await api("/admin/billing/settings", { method: "PUT", body: JSON.stringify(body) });
  await loadBilling();
  return "Tarifs enregistrés : ils s'appliquent dès maintenant.";
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
  // Quartier sélectionné (contour et itinéraires) : au-dessus de la zone.
  routeLayer = L.layerGroup().addTo(map);
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
  // Clic sur une choroplèthe : sélection du quartier IRIS (zone « Quartier sélectionné »).
  for (const l of [communeLayer, immoLayer, fiscLayer]) l.on("click", (e) => selectQuartier(e.latlng));
  securityLegend().addTo(map);
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
      setTimeout(() => {
        choros.forEach((l) => l !== e.layer && map.removeLayer(l));
        syncUrl();
      }, 0);
    }
    syncUrl();
  });
  // Niveau de détail : communes de loin, quartiers IRIS de près.
  // Leaflet ne recalcule pas la taille d'une popup quand un pli <details>
  // (résultats d'un établissement) s'ouvre ou se ferme : on la recale.
  map.on("popupopen", (e) => {
    const el = e.popup.getElement();
    if (!el) return;
    for (const d of el.querySelectorAll("details")) {
      if (d.dataset.popupFit) continue; // contenu réutilisé : un seul écouteur
      d.dataset.popupFit = "1";
      d.addEventListener("toggle", () => e.popup.update());
    }
  });
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
// Classe la plus claire ≥ 2:1 sur les tuiles OSM (#f2efe9), pour rester
// distincte du fond de carte : rampe vérifiée avec le validateur dataviz
// (--ordinal --surface "#f2efe9"). L'histogramme de la zone en reprend
// l'étape médiane (≥ 3:1 sur le panneau #14171f).
const SECURITY_CLASSES = [
  { min: 80, color: "#0d366b", label: "80–100 (plus sûre)" },
  { min: 60, color: "#1757ac", label: "60–79" },
  { min: 40, color: "#2370cd", label: "40–59" },
  { min: 20, color: "#4a8fe0", label: "20–39" },
  { min: 0, color: "#78aced", label: "0–19 (moins sûre)" },
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
    `diffusés par la commune comptent.</p>` +
    `<p>La carte affiche le rang de la commune <strong>dans son département</strong> ` +
    `(0 = la moins sûre du département, 100 = la plus sûre). Les départements sont ` +
    `classés entre eux par le score national moyen de leurs communes.</p>${groups}</span></span>`
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

// Nom d'un département de securite_departement : « Haute-Garonne (31) ».
function depName(d) {
  return d.nom ? `${d.nom} (${d.code})` : `Département ${d.code}`;
}

// Rang ordinal français : 1er, 2e…
function ordinal(n) {
  return n === 1 ? "1er" : `${n}e`;
}

// Classement national d'un département, en une ligne.
function depRankLine(d) {
  return (
    `<strong>${esc(depName(d))}</strong> : ${ordinal(d.rang)} / ${d.nb_departements} départements` +
    ` <span class="muted">(score moyen ${Math.round(d.score_moyen)}/100)</span>`
  );
}

// Classement national des départements des communes données (distincts,
// du plus sûr au moins sûr) ; vide sans données.
function departementsRankHtml(communes) {
  const deps = new Map();
  for (const c of communes || []) {
    if (c.departement) deps.set(c.departement.code, c.departement);
  }
  return [...deps.values()]
    .sort((a, b) => a.rang - b.rang)
    .map((d) => `<div>${depRankLine(d)}</div>`)
    .join("");
}

// Infobulle d'une commune : score départemental (affiché par la carte), puis
// score national.
function communeTooltip(p) {
  const nom = `<strong>${esc(p.nom)}</strong>`;
  if (p.score == null) return `${nom}<br>Sécurité : pas de données diffusées`;
  const dep = p.departement ? ` de ${esc(p.departement.nom || p.departement.code)}` : " du département";
  return (
    `${nom}<br>Sécurité : <strong>${p.score}/100</strong> dans le département` +
    `<br><span class="muted">plus sûre que ${p.score} % des communes${dep}` +
    `<br>score national ${p.score_national}/100 · ${p.annee}, ${p.nb_indicateurs}/15 indicateurs diffusés</span>`
  );
}

// ---------------------------------------------------------------------------
// Isochrones et zone de recherche
// ---------------------------------------------------------------------------
// Zone retenue : non remplie (choroplèthes lisibles), contour noir pointillé
// posé sur un liseré blanc qui le garde visible sur les classes foncées des
// choroplèthes.
const ZONE_STYLES = [
  { color: "#ffffff", weight: 5, opacity: 0.7, fill: false },
  { color: "#111111", weight: 2.5, opacity: 1, dashArray: "8 6", lineCap: "butt", fill: false },
];

function securityLegend() {
  const legend = L.control({ position: "bottomright" });
  legend.onAdd = () => {
    const div = L.DomUtil.create("div", "map-legend security-legend");
    const rows = SECURITY_CLASSES.map(
      (c) => `<li><span class="swatch" style="background:${c.color}"></span>${c.label}</li>`,
    ).join("");
    div.innerHTML =
      `<div class="legend-title">Sécurité (rang dans le département)</div>` +
      `<div class="legend-deps"></div><ul>${rows}` +
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
        // Score affiché : rang dans le département ; le national est rappelé.
        score: c.score_departement ?? null,
        score_national: c.score,
        departement: c.departement,
        annee: c.annee,
        nb_indicateurs: c.nb_indicateurs,
      },
    }));
  communeLayer.addData(features);
  // Classement national des départements de la zone, en tête de la légende.
  const deps = document.querySelector(".security-legend .legend-deps");
  if (deps) deps.innerHTML = departementsRankHtml(communes);
  $(".security-legend").hidden = !features.length || !map.hasLayer(communeLayer);
}

// ---------------------------------------------------------------------------
// Choroplèthe « prix au m² » (DVF, dernier millésime)
// ---------------------------------------------------------------------------
// En dessous de ce zoom, les prix sont agrégés par commune ; au-delà, détail
// par quartier IRIS.
const IRIS_MIN_ZOOM = 12;
// Rampe séquentielle orange/brun, du moins cher (clair) au plus cher (foncé).
// Classe la plus claire ≥ 2:1 sur les tuiles OSM (#f2efe9), pour rester
// distincte du fond de carte : rampe vérifiée avec le validateur dataviz
// (--ordinal --surface "#f2efe9"). L'histogramme de la zone en reprend
// l'étape médiane (≥ 3:1 sur le panneau #14171f).
const IMMO_COLORS = ["#e99649", "#d87524", "#bd5d18", "#984610", "#683006"];
let immoData = null; // data.immobilier de la dernière recherche
let immoCommunes = []; // data.communes (noms et contours des communes)
let immoLevel = null; // "commune" | "iris" (niveau actuellement affiché)
let immoScale = null; // { breaks, colors, min, max } de l'échelle courante
// Filtre de prix au m² de la dernière recherche (null : borne absente). Les
// quartiers IRIS hors de cet intervalle ne sont pas colorés.
let immoFiltre = { min: null, max: null };

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

// Prix au m² médian d'une entité, ou null sans vente ou hors filtre de prix
// (exclue de l'échelle de couleurs).
function prixFor(p) {
  if (p.horsFiltre) return null;
  return p.prix ? p.prix.prix_m2 : null;
}

// Indique si le prix d'un quartier IRIS (à défaut, celui de sa commune, comme
// côté serveur) ne respecte pas le filtre de prix ; un prix inconnu l'exclut
// dès qu'une borne est renseignée.
function horsFiltrePrix(prix) {
  const { min, max } = immoFiltre;
  if (min == null && max == null) return false;
  if (prix == null) return true;
  return (min != null && prix < min) || (max != null && prix > max);
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
  if (feature.properties.horsFiltre) {
    // Quartier exclu par le filtre de prix : contour seul, aucune couleur.
    return { color: "#a8a29e", weight: 0.6, opacity: 0.6, fill: true, fillOpacity: 0, dashArray: null };
  }
  const v = prixFor(feature.properties);
  return {
    fill: true,
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
  const hors = p.horsFiltre ? `<br><span class="muted">Hors du filtre de prix</span>` : "";
  return (
    `<strong>${esc(p.nom)}</strong>${sub}` +
    `<br>${prixLine(p.prix)}${hors}`
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
  const hors =
    immoLevel === "iris" && immoLayer.getLayers().some((l) => l.feature.properties.horsFiltre)
      ? `<li><span class="swatch excluded"></span>Hors filtre de prix</li>`
      : "";
  div.querySelector("ul").innerHTML = scaleLegendRows(immoScale, fmtPrix, (n) => n.toLocaleString("fr-FR")) + hors;
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
      const prixCommune = new Map(
        (immoData.communes || []).map((c) => [c.code_commune, c.prix ? c.prix.prix_m2 : null]),
      );
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
            horsFiltre: horsFiltrePrix(q.prix ? q.prix.prix_m2 : prixCommune.get(q.code_commune)),
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
// Classe la plus claire ≥ 2:1 sur les tuiles OSM (#f2efe9), pour rester
// distincte du fond de carte : rampe vérifiée avec le validateur dataviz
// (--ordinal --surface "#f2efe9"). L'histogramme de la zone en reprend
// l'étape médiane (≥ 3:1 sur le panneau #14171f).
const FISC_COLORS = ["#a9a3d4", "#8a82c2", "#6f63ae", "#56459a", "#3f1d78"];
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
// Quartier sélectionné : un clic sur une choroplèthe, ou le dépliage de son
// nœud dans l'arbre des quartiers, sélectionne un quartier IRIS (un seul à la
// fois) ; sa fiche (prix au m², sécurité, fiscalité, distance à vol d'oiseau à
// l'établissement le plus proche de chaque nature recherchée et itinéraires)
// s'affiche dans le nœud déplié.
// ---------------------------------------------------------------------------
let lastCriteres = []; // critères de la dernière recherche
let lastFiltres = {}; // filtres de zone de la dernière recherche
let placeSeq = 0; // ignore les réponses d'une sélection obsolète
const routeCache = new Map(); // "lon,lat|lon,lat|transp" → promesse de /itineraire

// Point (lon, lat) dans un anneau [[lon, lat], ...] (lancer de rayon).
function pointInRing(lon, lat, ring) {
  let inside = false;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const [xi, yi] = ring[i];
    const [xj, yj] = ring[j];
    if (yi > lat !== yj > lat && lon < ((xj - xi) * (lat - yi)) / (yj - yi) + xi) inside = !inside;
  }
  return inside;
}

// Point dans un polygone GeoJSON (anneau extérieur puis trous).
function pointInPolygon(lon, lat, rings) {
  if (!rings.length || !pointInRing(lon, lat, rings[0])) return false;
  return !rings.slice(1).some((hole) => pointInRing(lon, lat, hole));
}

function pointInGeometry(lon, lat, geom) {
  if (!geom) return false;
  if (geom.type === "Polygon") return pointInPolygon(lon, lat, geom.coordinates);
  if (geom.type === "MultiPolygon") return geom.coordinates.some((p) => pointInPolygon(lon, lat, p));
  return false;
}

function placeSection(title, body) {
  return `<div class="place-section"><div class="place-section-title">${title}</div>${body}</div>`;
}

// Distance lisible : « 850 m », « 1,3 km », « 24 km ».
function fmtDistance(m) {
  if (m < 1000) return `${Math.round(m / 10) * 10} m`;
  const km = m / 1000;
  return `${km.toLocaleString("fr-FR", { maximumFractionDigits: km < 10 ? 1 : 0 })} km`;
}

// Durée lisible : « 8 min », « 1 h 05 ».
function fmtDuree(s) {
  const min = Math.max(1, Math.round(s / 60));
  if (min < 60) return `${min} min`;
  return `${Math.floor(min / 60)} h ${String(min % 60).padStart(2, "0")}`;
}

const TRANSP_LABELS = { "driving-car": "en voiture", "cycling-regular": "à vélo", "foot-walking": "à pied" };

// Ligne « itinéraire » d'un établissement proche : undefined (en cours),
// null (indisponible) ou {duree_s, distance_m, transp}.
function itineraireLine(it) {
  if (it === undefined) return `<div class="muted place-proche">Itinéraire…</div>`;
  if (it === null) return `<div class="muted place-proche">Itinéraire indisponible</div>`;
  return `<div class="place-proche">Itinéraire ${TRANSP_LABELS[it.transp] || ""} : ` +
    `<strong>${fmtDuree(it.duree_s)}</strong> <span class="muted">(${fmtDistance(it.distance_m)})</span></div>`;
}

// Section « Établissements les plus proches » : contenu provisoire tant que
// la réponse du serveur n'est pas arrivée ; itineraires : nature → itinéraire
// (voir itineraireLine), absent si l'établissement n'a pas de position.
function prochesSection(proches, itineraires = new Map()) {
  let body;
  if (proches === undefined) {
    body = `<span class="muted">Chargement…</span>`;
  } else if (proches === null) {
    body = `<span class="muted">Distances indisponibles.</span>`;
  } else if (!proches.length) {
    body = `<span class="muted">Aucune nature recherchée.</span>`;
  } else {
    body = `<ul class="place-times">${proches
      .map((p) => {
        const label = natureLabel(p.nature);
        const val = p.distance_m == null
          ? `<span class="muted">aucun établissement</span>`
          : `<strong>${fmtDistance(p.distance_m)}</strong>`;
        const detail = p.nom
          ? `<div class="muted place-proche">${esc(p.nom)}${p.commune ? `, ${esc(p.commune)}` : ""}` +
            `${p.taux != null ? ` — ${fmtTaux(p.taux)}` : ""}</div>` +
            (itineraires.has(p.nature) || p.lon != null ? itineraireLine(itineraires.get(p.nature)) : "")
          : "";
        return `<li><span class="place-dot" style="background:${natureGroup(p.nature).color}"></span>` +
          `<span class="place-origin">${label}${detail}</span><span>${val}</span></li>`;
      })
      .join("")}</ul>`;
  }
  return placeSection(`Établissements les plus proches <span class="muted">(à vol d'oiseau)</span>`, body);
}

function placeCardHtml(quartier, proches, itineraires) {
  const communes = lastData.communes || [];
  const commune = communes.find((c) => c.code_commune === quartier.code_commune);
  const immo = immoData || {};
  let html = "";

  // Prix au m² : médiane de la commune et, si disponible, du quartier IRIS.
  const y0 = immo.date_min ? immo.date_min.slice(0, 4) : "";
  const y1 = immo.date_max ? immo.date_max.slice(0, 4) : "";
  const periode = y0 ? ` <span class="muted">(${y0 === y1 ? y0 : `${y0}–${y1}`})</span>` : "";
  let prix = "";
  if (commune) {
    const pc = (immo.communes || []).find((c) => c.code_commune === commune.code_commune);
    prix += `<div>Commune — ${prixLine(pc && pc.prix)}</div>`;
  }
  prix += `<div>Quartier — ${prixLine(quartier.prix)}</div>`;
  html += placeSection(`Prix au m²${periode}`, prix || `<span class="muted">pas de données</span>`);

  if (commune) {
    // Sécurité
    // Classement du département en tête, puis score départemental et national.
    const sd = commune.score_departement;
    const secu =
      commune.score == null || sd == null
        ? `<span class="muted">pas de données diffusées</span>`
        : (commune.departement ? `<div>${depRankLine(commune.departement)}</div>` : "") +
          `Score : <strong>${sd}/100</strong> dans le département` +
          `<div class="muted">plus sûre que ${sd} % des communes du département — ` +
          `score national ${commune.score}/100 — ` +
          `${commune.annee}, ${commune.nb_indicateurs}/15 indicateurs diffusés</div>`;
    html += placeSection("Sécurité", secu);

    // Fiscalité
    const f = commune.fiscalite;
    const fisc = !f
      ? `<span class="muted">pas de données</span>`
      : `TFB + TEOM : <strong>${fmtTaux(f.taux)}</strong>` +
        `<div class="muted">TFB ${fmtTaux(f.taux_tfb)}, TEOM ${
          f.taux_teom == null ? "pas de TEOM" : fmtTaux(f.taux_teom)
        } — exercice ${f.annee}</div>`;
    html += placeSection("Fiscalité locale", fisc);
  }

  html += prochesSection(proches, itineraires);
  return html;
}

// Nœud de l'arbre des quartiers d'un code IRIS (null s'il n'y est pas).
function quartierNode(iris) {
  return document.querySelector(`#quartier-tree .qt-node[data-iris="${CSS.escape(iris)}"]`);
}

function zoneQuartiers() {
  return (immoData && immoData.quartiers) || [];
}

// Désélectionne le quartier : nœud replié, contour et itinéraires effacés,
// réponses en attente ignorées.
function clearQuartier() {
  placeSeq++;
  if (routeLayer) routeLayer.clearLayers();
  if (openIris == null) return;
  const node = quartierNode(openIris);
  openIris = null;
  if (node) {
    node.classList.remove("open");
    node.querySelector(".qt-head").setAttribute("aria-expanded", "false");
    node.querySelector(".qt-body").innerHTML = "";
  }
  syncUrl();
}

// Clic sur la carte : sélectionne le quartier IRIS sous le point (aucun :
// désélection), déplie son nœud et le fait défiler à l'écran.
function selectQuartier(latlng) {
  if (!lastData) return;
  const { lng: lon, lat } = latlng;
  const quartier = zoneQuartiers().find((q) => pointInGeometry(lon, lat, q.contour));
  if (!quartier) {
    clearQuartier();
    return;
  }
  $("#data-fold").open = true;
  openQuartier(quartier);
  const node = quartierNode(quartier.iris);
  if (node) node.scrollIntoView({ block: "nearest", behavior: "smooth" });
}

// Sélectionne un quartier : nœud déplié (fiche + établissements), contour en
// pointillés ; les distances aux établissements sont demandées au serveur
// (non facturé), puis l'itinéraire du point du quartier vers chacun (mode de
// transport de son critère) est tracé sur la carte tant que le quartier reste
// sélectionné.
function openQuartier(quartier) {
  clearQuartier();
  const node = quartierNode(quartier.iris);
  if (!node) return;
  openIris = quartier.iris;
  node.classList.add("open");
  node.querySelector(".qt-head").setAttribute("aria-expanded", "true");
  openReachable = reachableSources(quartier);
  const n = openReachable.length;
  const body = node.querySelector(".qt-body");
  body.innerHTML =
    `<div class="qt-card"></div>` +
    `<details class="subfold qt-etabs"><summary>Établissements accessibles (${n})</summary>` +
    `<ul class="etab-list"></ul></details>`;
  const card = body.querySelector(".qt-card");
  card.innerHTML = placeCardHtml(quartier, undefined);
  renderQuartierEtabs();
  syncUrl();
  L.geoJSON(quartier.contour, {
    interactive: false,
    style: { color: "#f8fafc", weight: 2.5, dashArray: "6 4", fill: false },
  }).addTo(routeLayer);

  const seq = placeSeq;
  const current = () => seq === placeSeq;
  api(`/quartiers/${encodeURIComponent(quartier.iris)}/proximite`, {
    method: "POST",
    body: JSON.stringify({ criteres: lastCriteres }),
  })
    .catch(() => null)
    .then((fiche) => {
      if (!current()) return;
      const proches = fiche ? fiche.proches : null;
      const itineraires = new Map();
      const refresh = () => {
        if (current()) card.innerHTML = placeCardHtml(quartier, proches, itineraires);
      };
      refresh();
      if (!fiche) return;
      drawQuartierRoutes(fiche, itineraires, current, refresh);
    });
}

// Itinéraire (mis en cache) de from à to ([lon, lat]) pour un profil.
function fetchRoute(from, to, transp) {
  const key = `${from}|${to}|${transp}`;
  if (!routeCache.has(key)) {
    const req = api("/itineraire", {
      method: "POST",
      body: JSON.stringify({ depart: from, arrivee: to, transp }),
    });
    req.catch(() => routeCache.delete(key)); // échec : nouvel essai au prochain clic
    routeCache.set(key, req);
  }
  return routeCache.get(key);
}

// Pastilles des établissements les plus proches du quartier (fiche
// /proximite) : cherchés parmi tous les établissements, ils sont souvent hors
// de la zone et n'ont alors aucun marqueur ; on en pose un au bout de leur
// itinéraire (dans routeLayer, effacé avec la sélection). Un même
// établissement le plus proche pour Maternelle et Élémentaire est une école
// primaire : une seule pastille « P ».
function addProcheMarkers(proches) {
  const onMap = new Set(
    [...((lastData && lastData.etablissements) || []), ...((lastData && lastData.sources) || [])].map((e) => e.UAI),
  );
  const byUai = new Map();
  for (const p of proches) {
    if (!p.uai || onMap.has(p.uai)) continue;
    if (!byUai.has(p.uai)) byUai.set(p.uai, { p, natures: [] });
    byUai.get(p.uai).natures.push(p.nature);
  }
  for (const { p, natures } of byUai.values()) {
    const g = natureGroup(natures.length > 1 ? "Primaire" : p.nature);
    const html =
      `<div class="popup-title">${esc(p.nom)}</div>` +
      `<div>${[p.secteur, p.commune].filter(Boolean).map(esc).join(" · ")}</div>` +
      (p.taux != null ? `<div>Taux de réussite : <strong>${fmtTaux(p.taux)}</strong></div>` : "") +
      `<div class="popup-meta">Le plus proche du quartier pour : ${natures.map(natureLabel).join(", ")} ` +
      `(hors des établissements de la zone).</div>`;
    L.marker([p.lat, p.lon], { icon: etabIcon(g), title: `${p.nom} (${g.label})`, zIndexOffset: 1500 })
      .bindPopup(html)
      .addTo(routeLayer);
  }
}

// Trace les itinéraires du point du quartier (fiche /proximite) vers chaque
// établissement proche ; itineraires (nature → résultat) est complété au fil
// des réponses, puis refresh met la zone à jour. current() est faux dès que
// le quartier a été désélectionné ou remplacé.
function drawQuartierRoutes(fiche, itineraires, current, refresh) {
  const from = [fiche.lon, fiche.lat];
  const proches = (fiche.proches || []).filter((p) => p.lon != null && p.lat != null);
  if (!proches.length) return;
  L.circleMarker([fiche.lat, fiche.lon], {
    radius: 6,
    color: "#fff",
    weight: 2,
    fillColor: "#111827",
    fillOpacity: 1,
    interactive: false,
  }).addTo(routeLayer);
  addProcheMarkers(proches);
  for (const p of proches) {
    const critere = lastCriteres.find((c) => c.nature === p.nature);
    const transp = critere ? critere.transp : "driving-car";
    itineraires.set(p.nature, undefined);
    fetchRoute(from, [p.lon, p.lat], transp)
      .then((it) => ({ ...it, transp }))
      .catch(() => null)
      .then((it) => {
        if (!current()) return;
        itineraires.set(p.nature, it);
        if (it && it.geometry && it.geometry.length) {
          // Non interactif : un clic sur le tracé ne doit pas changer la sélection.
          const line = it.geometry.map(([x, y]) => [y, x]);
          L.polyline(line, {
            color: natureGroup(p.nature).color,
            weight: 4,
            opacity: 0.85,
            interactive: false,
          }).addTo(routeLayer);
          // Texture animée (points blancs, voir .route-flow) : elle avance du
          // quartier, origine du tracé, vers l'établissement.
          L.polyline(line, {
            color: "#ffffff",
            weight: 2,
            opacity: 0.95,
            dashArray: "1 11",
            lineCap: "round",
            className: "route-flow",
            interactive: false,
          }).addTo(routeLayer);
        }
        refresh();
      });
  }
  refresh();
}

// ---------------------------------------------------------------------------
// Critères par nature : pour chaque nature cochée, les établissements du rayon
// (tous, ou les meilleurs si le serveur plafonne les sources) donnent chacun une isochrone (temps et mode propres
// à la nature) ; la zone est l'intersection, entre natures, de l'union des
// isochrones de chaque nature (près d'au moins un établissement de chacune).
// ---------------------------------------------------------------------------
const SECTEURS = ["public", "privé", "hors contrat"];
// Filières d'un lycée (models.Filieres côté serveur).
const FILIERES = ["Général", "Technologique", "Professionnel", "Agricole"];
// Natures proposées (valeurs de etablissement.nature) et réglages par défaut ;
// taux : la nature a des résultats d'examens ; filieres : filtre par filière.
const CRITERE_NATURES = [
  { nature: "Maternelle", label: "Maternelle", taux: false, min: 10, transp: "foot-walking" },
  { nature: "Elementaire", label: "Élémentaire", taux: false, min: 10, transp: "foot-walking" },
  { nature: "Collège", label: "Collège", taux: true, min: 15, transp: "foot-walking" },
  { nature: "Lycée", label: "Lycée", taux: true, filieres: true, min: 20, transp: "driving-car" },
];

function natureLabel(nature) {
  const n = CRITERE_NATURES.find((c) => c.nature === nature);
  return n ? n.label : esc(nature);
}

function buildCriteres() {
  const box = $("#criteres");
  for (const n of CRITERE_NATURES) {
    const row = $("#critere-tpl").content.firstElementChild.cloneNode(true);
    row.dataset.nature = n.nature;
    row.querySelector(".critere-title").textContent = n.label;
    row.querySelector(".origin-dot").style.background = natureGroup(n.nature).color;
    row.querySelector(".critere-taux").hidden = !n.taux;
    row.querySelector(".critere-filieres").hidden = !n.filieres;
    const range = row.querySelector(".critere-time");
    range.addEventListener("input", () => {
      row.querySelector(".origin-time-val").textContent = range.value;
    });
    row.querySelector(".critere-on").addEventListener("change", (e) => {
      row.querySelector(".critere-body").hidden = !e.target.checked;
      updateSearchCost();
    });
    box.appendChild(row);
    // Réglages par défaut de la nature (repris quand on la coche) ; seuls
    // les lycées sont recherchés au départ.
    setCritere(row, {});
    if (n.nature !== "Lycée") setCritere(row, null);
  }
}

// Active (opts : {min, transp, secteur, taux: [min, max], filiere}, champs absents :
// valeurs par défaut de la nature) ou désactive (opts null, réglages
// conservés) un critère.
function setCritere(row, opts) {
  const n = CRITERE_NATURES.find((c) => c.nature === row.dataset.nature);
  const on = opts != null;
  row.querySelector(".critere-on").checked = on;
  row.querySelector(".critere-body").hidden = !on;
  if (!on) return;
  const range = row.querySelector(".critere-time");
  const min = Number.isFinite(opts.min) ? opts.min : n.min;
  range.value = Math.min(60, Math.max(5, Math.round(min / 5) * 5));
  row.querySelector(".origin-time-val").textContent = range.value;
  const select = row.querySelector(".critere-transp");
  const transp = opts.transp || n.transp;
  if ([...select.options].some((o) => o.value === transp)) select.value = transp;
  const secteurs = opts.secteur && opts.secteur.length ? opts.secteur : SECTEURS;
  row.querySelectorAll(".critere-secteurs input").forEach((cb) => (cb.checked = secteurs.includes(cb.value)));
  const filieres = opts.filiere && opts.filiere.length ? opts.filiere : FILIERES;
  row.querySelectorAll(".critere-filieres input").forEach((cb) => (cb.checked = filieres.includes(cb.value)));
  const [tmin, tmax] = opts.taux || [0, 100];
  row.querySelector(".taux-min").value = Math.min(100, Math.max(0, tmin));
  row.querySelector(".taux-max").value = Math.min(100, Math.max(0, tmax));
}

// Critères des natures cochées, au format de l'API. filiere n'est transmis que
// si toutes les filières ne sont pas cochées (sinon pas de filtre : les lycées
// sans filière connue restent retenus).
function collectCriteres() {
  return Array.from(document.querySelectorAll("#criteres .critere-row"))
    .filter((row) => row.querySelector(".critere-on").checked)
    .map((row) => {
      const n = CRITERE_NATURES.find((c) => c.nature === row.dataset.nature);
      const num = (sel, def) => {
        const v = parseFloat(row.querySelector(sel).value);
        return Number.isFinite(v) ? Math.min(100, Math.max(0, v)) : def;
      };
      const filiere = n.filieres
        ? Array.from(row.querySelectorAll(".critere-filieres input:checked")).map((cb) => cb.value)
        : [];
      return {
        nature: n.nature,
        dist: parseInt(row.querySelector(".critere-time").value, 10) * 60, // minutes → secondes
        transp: row.querySelector(".critere-transp").value,
        secteur: Array.from(row.querySelectorAll(".critere-secteurs input:checked")).map((cb) => cb.value),
        taux_min: n.taux ? num(".taux-min", 0) : 0,
        taux_max: n.taux ? num(".taux-max", 100) : 100,
        ...(n.filieres && filiere.length < FILIERES.length ? { filiere } : {}),
      };
    });
}

// Filtres de zone saisis (null : pas de filtre).
function collectFiltres() {
  const num = (id) => {
    const v = parseFloat($(id).value);
    return Number.isFinite(v) ? v : null;
  };
  return {
    prix_m2_min: num("#prix-min"),
    prix_m2_max: num("#prix-max"),
    securite_min: num("#securite-min"),
    fiscalite_max: num("#fiscalite-max"),
  };
}

$("#rayon").addEventListener("input", () => {
  $("#rayon-val").textContent = $("#rayon").value;
});

$("#logout").addEventListener("click", async () => {
  // Révoque le token côté serveur ; on se déconnecte localement quoi qu'il arrive.
  try {
    if (token) await api("/auth/logout", { method: "POST" });
  } catch (_) {
    /* token déjà invalide ou serveur injoignable */
  }
  setToken(null);
  if (map) map.closePopup();
  clearQuartier();
  if (isoLayer) isoLayer.clearLayers();
  if (markerLayer) markerLayer.clearLayers();
  if (communeLayer) renderCommunes([]);
  immoFiltre = { min: null, max: null };
  if (immoLayer) renderImmobilier(null, []);
  if (fiscLayer) renderFiscalite([]);
  if (logementLayer) renderLogements([]);
  lastData = null;
  billing = null;
  renderBilling();
  renderInsights([]);
  renderZoneCharts();
  $("#results-summary").hidden = true;
  $("#search-status").textContent = "";
  $("#password").value = "";
  showAuth();
  applyMode();
});

$("#search-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const status = $("#search-status");
  const btn = $("#search-btn");
  status.classList.remove("err");

  const fail = (msg) => {
    status.textContent = msg;
    status.classList.add("err");
  };
  const centre = $("#centre").value.trim();
  if (!centre) return fail("Indiquez le centre de la recherche.");
  const criteres = collectCriteres();
  if (!criteres.length) return fail("Cochez au moins une nature d'établissement.");
  for (const c of criteres) {
    if (!c.secteur.length) return fail(`${natureLabel(c.nature)} : cochez au moins un secteur.`);
    if (c.filiere && !c.filiere.length) return fail(`${natureLabel(c.nature)} : cochez au moins une filière.`);
    if (c.taux_min > c.taux_max) return fail(`${natureLabel(c.nature)} : taux minimum supérieur au maximum.`);
  }
  const filtres = collectFiltres();
  if (filtres.prix_m2_min != null && filtres.prix_m2_max != null && filtres.prix_m2_min > filtres.prix_m2_max) {
    return fail("Prix au m² : minimum supérieur au maximum.");
  }

  btn.disabled = true;
  const body = { centre, rayon_km: parseInt($("#rayon").value, 10), criteres, ...filtres };
  try {
    // Estimation (gratuite) : nombre d'isochrones et coût, confirmation si le
    // coût dépasse le seuil d'avertissement, arrêt si le solde ne suffit pas.
    status.textContent = "Estimation du coût…";
    const est = await api("/etablissements/estimation", { method: "POST", body: JSON.stringify(body) });
    const resume = estimationText(est);
    if (!est.gratuit && est.cout_cents > est.solde_cents) {
      const err = new Error(`${resume} — crédit insuffisant (solde ${fmtMoney(est.solde_cents)}).`);
      err.code = "insufficient_credit";
      throw err;
    }
    if (!est.gratuit && est.avertissement_cents > 0 && est.cout_cents > est.avertissement_cents &&
        !window.confirm(`Coût élevé : ${resume}.\nSolde : ${fmtMoney(est.solde_cents)}.\n\nLancer la recherche ?`)) {
      status.textContent = `Recherche annulée (${resume}).`;
      return;
    }
    status.textContent = `Recherche en cours… ${resume}`;
    const data = await api("/etablissements", {
      method: "POST",
      body: JSON.stringify(body),
    });
    lastCriteres = criteres;
    lastFiltres = filtres;
    immoFiltre = { min: filtres.prix_m2_min, max: filtres.prix_m2_max };
    render(data);
    // Recherche aboutie : place à la carte et aux données de la zone. Zone
    // vide : les paramètres restent dépliés pour lire l'explication.
    if (data.zone) {
      $("#search-fold").open = false;
      $("#data-fold").open = true;
    }
  } catch (ex) {
    status.textContent = ex.message;
    status.classList.add("err");
    if (ex.code === "insufficient_credit") {
      const topup = document.createElement("button");
      topup.type = "button";
      topup.className = "btn-ghost status-action";
      topup.textContent = "Recharger";
      topup.addEventListener("click", () => openBilling());
      status.append(" ", topup);
    }
  } finally {
    btn.disabled = false;
    if (billing && billing.enabled) loadBilling(); // débit ou remboursement
  }
});

// ---------------------------------------------------------------------------
// Autocomplétion de l'adresse du centre de la recherche
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
    syncUrl();
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

// Après la section d'autocomplétion (acSeq doit être initialisé).
setupAddressAutocomplete($("#centre"), $("#centre-wrap .ac-list"));

// Saisie dans le formulaire (centre, rayon, critères, filtres) : URL et coût à jour.
for (const type of ["input", "change"]) {
  $("#search-form").addEventListener(type, () => {
    syncUrl();
    updateSearchCost();
  });
}

function render(data) {
  map.closePopup(); // popup d'un établissement de la recherche précédente
  clearQuartier(); // quartier sélectionné dans la recherche précédente
  isoLayer.clearLayers();
  markerLayer.clearLayers();
  renderCommunes(data.communes);
  renderImmobilier(data.immobilier, data.communes);
  renderFiscalite(data.communes);
  renderLogements(data.logements_sociaux || []);

  const isoBounds = L.latLngBounds([]);
  // Centre et rayon de la recherche.
  if (data.centre) {
    const c = [data.centre.lat, data.centre.lon];
    const circle = L.circle(c, {
      radius: data.centre.rayon_km * 1000,
      color: "#64748b",
      weight: 1.5,
      dashArray: "4 6",
      fill: false,
      interactive: false,
    }).addTo(isoLayer);
    isoBounds.extend(circle.getBounds());
    L.circleMarker(c, { radius: 7, color: "#334155", fillColor: "#334155", fillOpacity: 0.9 })
      .bindPopup("Centre de la recherche")
      .addTo(isoLayer);
  }

  // Seule la zone retenue est tracée ; les isochrones des établissements
  // sources (geometry = [[lon, lat], ...]) servent au cadrage si elle est vide.
  const sources = data.sources || [];
  for (const src of sources) {
    if (Array.isArray(src.isochrone) && src.isochrone.length) {
      isoBounds.extend(L.latLngBounds(src.isochrone.map(([lon, lat]) => [lat, lon])));
    }
  }

  let zoneBounds = null;
  if (data.zone) {
    for (const style of ZONE_STYLES) {
      const zone = L.geoJSON(data.zone, { style, interactive: false }).addTo(isoLayer);
      zoneBounds = zone.getBounds();
    }
  }

  // Établissements sources : pastille à la couleur de la nature du critère.
  sourceMarkerByUai = new Map();
  for (const src of sources) {
    const coords = src.position && src.position.coordinates;
    if (!coords) continue;
    const g = natureGroup(sourceNature(src));
    const marker = L.marker([coords[1], coords[0]], {
      icon: etabIcon(g, true),
      title: `Source (${g.label})`,
      zIndexOffset: 2000, // au-dessus des établissements de la zone
    })
      .bindPopup(() => sourcePopupHtml(src))
      .addTo(isoLayer);
    sourceMarkerByUai.set(src.UAI, marker);
  }

  lastData = data;
  renderMarkers();
  renderZoneCharts();

  // Zone vide : explication.
  const status = $("#search-status");
  let empty = "";
  if ((data.criteres_vides || []).length) {
    empty = `Aucun établissement retenu pour : ${data.criteres_vides.map(natureLabel).join(", ")}. ` +
      "Élargissez le rayon ou assouplissez les critères de cette nature.";
  } else if (!data.zone_brute) {
    empty = `Les ${sources.length} isochrones n'ont aucune zone commune. ` +
      "Augmentez les temps de trajet, réduisez le rayon ou le nombre de natures, ou relevez le taux minimum.";
  } else if (!data.zone) {
    empty = "Aucun quartier de la zone ne respecte les filtres de zone (prix, sécurité, fiscalité).";
  }
  if (empty) {
    status.textContent = empty;
    status.classList.add("err");
  }

  showMapOnNarrow();
  const fit = zoneBounds && zoneBounds.isValid() ? zoneBounds : isoBounds;
  if (fit.isValid()) map.fitBounds(fit, { padding: [40, 40], maxZoom: 14 });
}

// Popup d'un établissement source : fiche, et paramètres de son isochrone.
// Nature du critère pour lequel une source a été retenue (sa propre nature
// pour une réponse sans « critere »).
function sourceNature(src) {
  return src.critere || src.nature;
}

function sourcePopupHtml(src) {
  return popupHtml(src) +
    `<div class="popup-meta">Établissement source (critère ${natureLabel(sourceNature(src))}) : isochrone de ${Math.round(src.dist / 60)} min ` +
    `${TRANSP_LABELS[src.transp] || esc(src.transp)}.</div>`;
}

// Pastille ronde d'un établissement, à la couleur de sa nature, portant son
// initiale (la couleur seule ne suffit pas à distinguer les natures sur la
// carte), anneau blanc. Source (son isochrone définit la zone) : un peu plus
// grande.
function etabIcon(g, source = false) {
  const size = source ? 24 : 20;
  return L.divIcon({
    className: source ? "etab-pin source" : "etab-pin",
    html: `<span style="background:${g.color};color:${g.ink || "#fff"}">${g.initial}</span>`,
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
    popupAnchor: [0, -size / 2],
  });
}

// (Re)dessine les marqueurs des établissements de la zone ; renvoie leurs
// positions.
function renderMarkers() {
  const bounds = [];
  if (!markerLayer || !lastData) return bounds;
  markerLayer.clearLayers();

  const etabs = lastData.etablissements || [];
  markerByEtab = new Map();
  for (const etab of etabs) {
    const coords = etab.position && etab.position.coordinates;
    if (!coords) continue;
    const [lon, lat] = coords;
    bounds.push([lat, lon]);
    const g = natureGroup(etab.nature);
    const marker = L.marker([lat, lon], { icon: etabIcon(g), title: `${etab.nom} (${g.label})` })
      .bindPopup(popupHtml(etab))
      .addTo(markerLayer);
    markerByEtab.set(etab, marker);
  }
  renderInsights(etabs);

  const status = $("#search-status");
  status.classList.remove("err");
  status.textContent =
    `${etabs.length} établissement(s) dans la zone, ${(lastData.sources || []).length} établissement(s) source(s).`;
  const nbLogements = (lastData.logements_sociaux || []).reduce((n, p) => n + p.nb_logements, 0);
  if (nbLogements) status.textContent += ` ${nbLogements} logement(s) social(aux).`;
  return bounds;
}

// ---------------------------------------------------------------------------
// Taux de réussite (Admis / Présents)
// ---------------------------------------------------------------------------

// Taux global d'un établissement en % (Σ admis / Σ présents), ou null. Même
// définition que la vue etablissement_taux (migration 017), à garder synchronisées.
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

function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"]/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])
  );
}

// Lien externe (http/https uniquement ; https:// ajouté à une adresse sans
// schéma, ex. « www.exemple.fr »), libellé échappé.
function extLink(url, label) {
  url = (url || "").trim();
  if (url && !/^[a-z][a-z0-9+.-]*:/i.test(url)) url = "https://" + url;
  if (!/^https?:\/\/[^\s]+$/i.test(url)) return "";
  return `<a href="${esc(url)}" target="_blank" rel="noopener">${esc(label)}</a>`;
}

// Nom d'hôte d'une URL, sans « www. » (l'URL elle-même si elle est invalide).
function hostLabel(url) {
  try {
    const u = new URL(/^https?:\/\//i.test(url) ? url : "https://" + url);
    return u.hostname.replace(/^www\./i, "");
  } catch (_) {
    return url;
  }
}

// Attributs de l'annuaire de l'éducation, en liste libellée : une ligne par
// information présente (booléen non nul : « Oui » / « Non »). Téléphone,
// e-mail, site web et fiche ONISEP sont cliquables.
function annuaireHtml(etab) {
  const rows = [];
  const add = (label, html) => {
    if (html) rows.push(`<dt>${esc(label)}</dt><dd>${html}</dd>`);
  };
  const yesNo = (v) => (v == null ? "" : v ? "Oui" : "Non");
  add("Éducation prioritaire", esc(etab.education_prioritaire || ""));
  add("Restauration", yesNo(etab.restauration));
  add("Internat", yesNo(etab.hebergement));
  add("ULIS", yesNo(etab.ulis));
  add("SEGPA", yesNo(etab.segpa));
  add("Apprentissage", yesNo(etab.apprentissage));
  add("Post-bac", yesNo(etab.post_bac));
  add("Lycée des métiers", yesNo(etab.lycee_des_metiers));
  add("Sections", esc((etab.sections || []).join(", ")));
  if (etab.telephone) {
    const tel = etab.telephone.replace(/[^\d+]/g, "");
    add("Téléphone", tel ? `<a href="tel:${esc(tel)}">${esc(etab.telephone)}</a>` : esc(etab.telephone));
  }
  if (etab.mail) add("E-mail", `<a href="mailto:${esc(etab.mail.trim())}">${esc(etab.mail)}</a>`);
  if (etab.site_web) add("Site web", extLink(etab.site_web, hostLabel(etab.site_web.trim())));
  add("Fiche ONISEP", extLink(etab.fiche_onisep, "Voir la fiche"));
  return rows.length ? `<dl class="popup-dl">${rows.join("")}</dl>` : "";
}

function popupHtml(etab) {
  let html = `<div class="popup-title">${esc(etab.nom)}</div>`;
  const meta = [etab.nature, etab.secteur, (etab.filieres || []).join(", ")].filter(Boolean).join(" · ");
  const addr = [etab.adresse, etab.code_postal, etab.commune].filter(Boolean).join(", ");
  html += `<div class="popup-meta">${esc(meta)}<br/>${esc(addr)}`;
  if (etab.UAI) html += `<br/>UAI : ${esc(etab.UAI)}`;
  html += `</div>`;
  html += annuaireHtml(etab);

  const res = etab.resultats || [];
  const rate = successRate(etab);
  if (rate != null) {
    html += `<div class="popup-meta">Taux de réussite : <strong>${rate.toFixed(1)} %</strong></div>`;
  }
  if (res.length) {
    // Résultats repliés par défaut (la popup est recalée à l'ouverture du pli,
    // voir l'écouteur « popupopen »).
    html += `<details class="subfold popup-fold"><summary>Résultats aux examens (${Math.min(res.length, 8)})</summary>`;
    html += `<table class="popup-table"><thead><tr>
      <th>Diplôme</th><th>Année</th><th>Présents</th><th>Admis</th><th>Mentions</th>
      </tr></thead><tbody>`;
    for (const r of res.slice(0, 8)) {
      html += `<tr><td>${esc(r.diplome)}</td><td>${esc(r.annee)}</td>
        <td>${esc(r.presents)}</td><td>${esc(r.admis)}</td><td>${esc(r.mentions)}</td></tr>`;
    }
    html += `</tbody></table></details>`;
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
// Données de la zone : statistiques générales (répartition par nature, taux,
// histogrammes) et arbre des quartiers
// ---------------------------------------------------------------------------
let markerByEtab = new Map();
let sourceMarkerByUai = new Map(); // UAI → marqueur d'un établissement source
let insightEtabs = []; // établissements de la zone affichés
let rateSel = new Set(); // tranches (0–9) sélectionnées dans l'histogramme des taux

// Tranche de 10 % du taux de réussite d'un établissement, ou null sans taux.
function rateBin(e) {
  const r = successRate(e);
  return r == null ? null : Math.min(9, Math.floor(r / 10));
}

// Établissement retenu par la sélection de l'histogramme (tous si aucune).
function inRateSel(e) {
  return !rateSel.size || rateSel.has(rateBin(e));
}

// Sur téléphone, la carte, les filtres et la liste sont empilés : on ramène
// la carte à l'écran quand une action la met à jour.
const narrowQuery = window.matchMedia("(max-width: 720px)");
function showMapOnNarrow() {
  if (narrowQuery.matches) $(".map-wrap").scrollIntoView({ behavior: "smooth", block: "start" });
}

// ---------------------------------------------------------------------------
// Largeur du volet de gauche, réglable à la souris (poignée entre le volet et
// la carte) ou au clavier ; préférence d'affichage conservée dans le
// navigateur (hors de l'URL partagée).
// ---------------------------------------------------------------------------
const PANEL_W_KEY = "douceville_panel_w";
const PANEL_W_DEFAULT = 340;
const PANEL_W_MIN = 260;

// Applique une largeur (bornée à [PANEL_W_MIN, 60 % de la fenêtre]) ; renvoie
// la largeur retenue.
function setPanelWidth(w) {
  const max = Math.max(PANEL_W_MIN, Math.round(window.innerWidth * 0.6));
  const width = Math.round(Math.min(max, Math.max(PANEL_W_MIN, w)));
  $(".layout").style.setProperty("--panel-w", `${width}px`);
  $(".panel-resizer").setAttribute("aria-valuenow", width);
  return width;
}

function savePanelWidth(w) {
  try {
    localStorage.setItem(PANEL_W_KEY, String(w));
  } catch {
    /* stockage indisponible : préférence non conservée */
  }
}

let mapResizeFrame = 0;
// Redimensionne la carte au plus une fois par image.
function refreshMapSize() {
  if (mapResizeFrame) return;
  mapResizeFrame = requestAnimationFrame(() => {
    mapResizeFrame = 0;
    if (map) map.invalidateSize();
  });
}

{
  const resizer = $(".panel-resizer");
  resizer.setAttribute("aria-valuemin", PANEL_W_MIN);
  let saved = NaN;
  try {
    saved = parseInt(localStorage.getItem(PANEL_W_KEY), 10);
  } catch {
    /* stockage indisponible */
  }
  setPanelWidth(Number.isFinite(saved) ? saved : PANEL_W_DEFAULT);

  let drag = null; // { x, w } au début du glissement
  resizer.addEventListener("pointerdown", (ev) => {
    if (ev.button !== 0) return;
    drag = { x: ev.clientX, w: $(".panel").getBoundingClientRect().width };
    resizer.setPointerCapture(ev.pointerId);
    document.body.classList.add("resizing");
    ev.preventDefault();
  });
  resizer.addEventListener("pointermove", (ev) => {
    if (!drag) return;
    setPanelWidth(drag.w + ev.clientX - drag.x);
    refreshMapSize();
  });
  const end = () => {
    if (!drag) return;
    drag = null;
    document.body.classList.remove("resizing");
    savePanelWidth($(".panel").getBoundingClientRect().width);
    refreshMapSize();
  };
  resizer.addEventListener("pointerup", end);
  resizer.addEventListener("pointercancel", end);
  resizer.addEventListener("dblclick", () => {
    savePanelWidth(setPanelWidth(PANEL_W_DEFAULT));
    refreshMapSize();
  });
  resizer.addEventListener("keydown", (ev) => {
    const step = ev.key === "ArrowLeft" ? -20 : ev.key === "ArrowRight" ? 20 : 0;
    if (!step) return;
    ev.preventDefault();
    savePanelWidth(setPanelWidth($(".panel").getBoundingClientRect().width + step));
    refreshMapSize();
  });
}

// Couleur attachée à la nature (jamais au rang) : ordre catégoriel fixe.
// Palette vérifiée avec le validateur dataviz : paires adjacentes sur le
// panneau (#14171f) et toutes les paires sur les tuiles OSM (#f2efe9), où
// l'initiale des marqueurs source sert d'encodage secondaire (ink : couleur
// de l'initiale quand le blanc manque de contraste).
const NATURE_GROUPS = [
  { key: "matern", label: "Maternelle", color: "#3987e5", initial: "M" },
  { key: "elem", label: "Élémentaire", color: "#d4492f", initial: "É" },
  // Écoles primaires (nature « Primaire ») : retenues par les critères
  // maternelle et élémentaire (voir critereMatch dans internal/store/search.go).
  { key: "primaire", label: "Primaire", color: "#8e44ad", initial: "P" },
  { key: "coll", label: "Collège", color: "#199e70", initial: "C" },
  { key: "lycee", label: "Lycée", color: "#c49600", initial: "L", ink: "#0b0b0b" },
];
const OTHER_GROUP = { key: "", label: "Autres", color: "#6b7280", initial: "A" };

function natureGroup(nature) {
  const n = String(nature || "").normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase();
  return NATURE_GROUPS.find((g) => n.includes(g.key)) || OTHER_GROUP;
}

const EMPTY_HINT = `<p class="empty">Lancez une recherche pour afficher les statistiques.</p>`;

// Infobulle partagée des graphiques : tout élément portant data-tip l'affiche
// au survol comme au focus clavier (elle complète l'aria-label et la vue
// tableau, sans s'y substituer).
const chartTip = document.createElement("div");
chartTip.className = "chart-tip";
chartTip.setAttribute("role", "tooltip");
chartTip.hidden = true;
document.body.appendChild(chartTip);

function showChartTip(target) {
  chartTip.textContent = target.dataset.tip;
  chartTip.hidden = false;
  const r = target.getBoundingClientRect();
  const w = chartTip.offsetWidth;
  const h = chartTip.offsetHeight;
  const x = Math.min(Math.max(4, r.left + r.width / 2 - w / 2), window.innerWidth - w - 4);
  const y = r.top - h - 6 < 4 ? r.bottom + 6 : r.top - h - 6;
  chartTip.style.left = `${x}px`;
  chartTip.style.top = `${y}px`;
}

function hideChartTip() {
  chartTip.hidden = true;
}

for (const type of ["pointerover", "focusin"]) {
  document.addEventListener(type, (ev) => {
    const t = ev.target.closest && ev.target.closest("[data-tip]");
    if (t) showChartTip(t);
  });
}
for (const type of ["pointerout", "focusout"]) {
  document.addEventListener(type, (ev) => {
    if (ev.target.closest && ev.target.closest("[data-tip]")) hideChartTip();
  });
}
window.addEventListener("scroll", hideChartTip, true);

// Vue tableau d'un graphique (équivalent accessible) : head = [colonne
// libellé, colonne valeur], rows = [[libellé, valeur], …].
function chartTable(head, rows) {
  const tr = (cells, tag) => `<tr>${cells.map((c) => `<${tag}>${c}</${tag}>`).join("")}</tr>`;
  return (
    `<details class="chart-table"><summary>Tableau</summary><table>` +
    `<thead>${tr(head, "th")}</thead><tbody>${rows.map((r) => tr(r, "td")).join("")}</tbody>` +
    `</table></details>`
  );
}

// Colonnes d'un histogramme. bins : effectifs ; label(i, n) : libellé de la
// tranche i ; slot : "button" (cliquable) ou "span" (statique) ; color : couleur
// des barres (défaut : accent) ; dimmed(i) : tranche atténuée.
function histogramBars(bins, { label, slot, color, dimmed }) {
  const max = Math.max(...bins);
  return bins
    .map((n, i) => {
      const h = n ? Math.max(2, (100 * n) / max) : 0;
      const tip = esc(label(i, n));
      const bg = color ? `;background:${color}` : "";
      const attrs = `data-bin="${i}" data-tip="${tip}" aria-label="${tip}"`;
      const bar = `<span class="bar" style="height:${h}%${bg}"></span>`;
      return slot === "button"
        ? `<button type="button" class="bar-slot" ${attrs} aria-pressed="false">${bar}</button>`
        : `<span class="bar-slot static${dimmed && dimmed(i) ? " dimmed" : ""}" tabindex="0" role="img" ${attrs}>${bar}</span>`;
    })
    .join("");
}

// Axe des abscisses d'un histogramme de n tranches : ticks = [[indice de
// borne (0…n), libellé], …], placés en proportion de la largeur.
function histogramAxis(n, ticks) {
  return (
    `<div class="bars-axis">` +
    ticks
      .map(([i, t]) => {
        const edge = i === 0 ? " first" : i === n ? " last" : "";
        return `<span class="tick${edge}" style="left:${(100 * i) / n}%">${t}</span>`;
      })
      .join("") +
    `</div>`
  );
}

function renderInsights(etabs) {
  renderNatureChart(etabs);
  renderRateChart(etabs);
  insightEtabs = etabs;
  renderQuartierTree();
  // Nouvel ensemble d'établissements : sélection effacée, sauf celle lue
  // dans l'URL, appliquée aux premiers résultats.
  rateSel = new Set();
  if (etabs.length && pendingRateSel) {
    rateSel = pendingRateSel;
    pendingRateSel = null;
  }
  applyRateSelection();
}

// Applique la sélection de l'histogramme des taux (façon Power BI) : barres,
// meilleurs taux (restreints à la sélection), liste et marqueurs mis en avant.
function applyRateSelection() {
  const active = rateSel.size > 0;
  document.querySelectorAll("#rate-chart .bar-slot").forEach((slot) => {
    const on = rateSel.has(Number(slot.dataset.bin));
    slot.classList.toggle("selected", on);
    slot.classList.toggle("dimmed", active && !on);
    slot.setAttribute("aria-pressed", on);
  });
  const clear = $("#rate-chart .bars-clear");
  if (clear) clear.hidden = !active;

  renderQuartierEtabs();

  for (const [e, m] of markerByEtab) {
    const on = inRateSel(e);
    m.setOpacity(on ? 1 : 0.25);
    m.setZIndexOffset(active && on ? 1000 : 0);
  }
  syncUrl();
}

// Clic sur une barre : sélection simple ; Ctrl/⌘ + clic : ajout/retrait.
$("#rate-chart").addEventListener("click", (ev) => {
  if (ev.target.closest(".bars-clear")) {
    rateSel.clear();
    applyRateSelection();
    return;
  }
  const slot = ev.target.closest(".bar-slot");
  if (!slot) return;
  const bin = Number(slot.dataset.bin);
  if (ev.ctrlKey || ev.metaKey) {
    if (rateSel.has(bin)) rateSel.delete(bin);
    else rateSel.add(bin);
  } else if (rateSel.size === 1 && rateSel.has(bin)) {
    rateSel.clear();
  } else {
    rateSel = new Set([bin]);
  }
  applyRateSelection();
});

// Répartition par nature : total en chiffre vedette, barre segmentée fine
// (part-à-tout lu d'un coup d'œil) et légende qui porte noms, effectifs et parts.
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
  const pct = (n) => `${Math.round((100 * n) / total)} %`;
  const segs = groups
    .map((g) => {
      const n = counts.get(g);
      const tip = `${g.label} : ${n} (${pct(n)})`;
      return (
        `<span class="seg" tabindex="0" role="img" data-tip="${tip}" aria-label="${tip}"` +
        ` style="flex-grow:${n};background:${g.color}"></span>`
      );
    })
    .join("");
  const legend = groups
    .map((g) => {
      const n = counts.get(g);
      return (
        `<li><span class="dot" style="background:${g.color}"></span>` +
        `<span class="lg-name">${g.label}</span>` +
        `<span class="lg-val">${n}</span><span class="lg-val lg-pct">${pct(n)}</span></li>`
      );
    })
    .join("");
  el.innerHTML =
    `<div class="stat"><span class="stat-num">${total.toLocaleString("fr-FR")}</span>` +
    `<span class="stat-unit">établissement${total > 1 ? "s" : ""}</span></div>` +
    `<div class="segbar">${segs}</div>` +
    `<ul class="chart-legend">${legend}</ul>`;
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
  const range = (i) => `${i * 10}–${i * 10 + 10} %`;
  const bars = histogramBars(bins, {
    slot: "button",
    label: (i, n) => `${range(i)} : ${n} établissement(s)`,
  });
  const rows = bins.map((n, i) => [range(i), n]);
  if (none) rows.push(["sans résultats", none]);
  el.innerHTML =
    `<div class="bars-head"><span>Établissements par tranche de taux</span>` +
    `<span><button type="button" class="bars-clear" hidden>Effacer la sélection</button> max ${max}</span></div>` +
    `<div class="bars">${bars}</div>` +
    // Unité sur la dernière graduation seulement : lisible dans un volet étroit.
    histogramAxis(10, [0, 2, 4, 6, 8, 10].map((i) => [i, i === 10 ? "100 %" : String(i * 10)])) +
    (none ? `<p class="chart-note">${none} sans résultats d'examen</p>` : "") +
    chartTable(["Taux de réussite", "Établissements"], rows);
}

// ---------------------------------------------------------------------------
// Histogrammes de la zone : sécurité et fiscalité des communes, prix au m²
// des quartiers IRIS (affichés même sans filtre ; les tranches hors du filtre
// de la dernière recherche sont atténuées).
// ---------------------------------------------------------------------------
// Plus petit pas de steps donnant au plus ~10 tranches sur l'étendue.
function niceStep(range, steps) {
  return steps.find((st) => range / st <= 10) || steps[steps.length - 1];
}

// values : valeurs (null : sans donnée) ; opts : {lo, hi, step} bornes fixes
// (sinon tirées des valeurs), steps (pas candidats), tickEvery (graduation
// toutes les k tranches ; défaut : début, milieu, fin), fmt (libellé d'une
// borne), unit (entités comptées), color, outside(a, b) (tranche [a, b[ hors
// filtre), filtre (libellé du filtre actif ou "").
function renderHistogram(el, values, opts) {
  if (!lastData) {
    el.innerHTML = EMPTY_HINT;
    return;
  }
  const known = values.filter((v) => v != null);
  const none = values.length - known.length;
  if (!known.length) {
    el.innerHTML = `<p class="empty">Aucune donnée pour les ${opts.unit} de la zone.</p>`;
    return;
  }
  let { lo, hi } = opts;
  let step = opts.step;
  if (lo == null) {
    const min = Math.min(...known);
    const max = Math.max(...known);
    step = niceStep(Math.max(max - min, opts.steps[0]), opts.steps);
    lo = Math.floor(min / step) * step;
    hi = Math.max(lo + step, Math.floor(max / step) * step + step);
  }
  const n = Math.round((hi - lo) / step);
  const bins = new Array(n).fill(0);
  for (const v of known) bins[Math.min(n - 1, Math.max(0, Math.floor((v - lo) / step)))]++;
  const max = Math.max(...bins);
  const range = (i) => `${opts.fmt(lo + i * step)} – ${opts.fmt(lo + (i + 1) * step)}`;
  const bars = histogramBars(bins, {
    slot: "span",
    color: opts.color,
    label: (i, c) => `${range(i)} : ${c} ${opts.unit}`,
    dimmed: opts.outside ? (i) => opts.outside(lo + i * step, lo + (i + 1) * step) : null,
  });
  const idx = opts.tickEvery
    ? Array.from({ length: Math.floor(n / opts.tickEvery) + 1 }, (_, k) => k * opts.tickEvery)
    : [...new Set([0, Math.floor(n / 2), n])];
  const notes = [];
  if (opts.filtre) notes.push(`Filtre : ${opts.filtre} (tranches hors filtre atténuées)`);
  if (none) notes.push(`${none} ${opts.unit} sans donnée`);
  const rows = bins.map((c, i) => [range(i), c]);
  if (none) rows.push(["sans donnée", none]);
  el.innerHTML =
    `<div class="bars-head"><span>${known.length} ${opts.unit}</span><span>max ${max}</span></div>` +
    `<div class="bars" aria-label="Histogramme : ${known.length} ${opts.unit}">${bars}</div>` +
    histogramAxis(n, idx.map((i) => [i, opts.fmt(lo + i * step)])) +
    notes.map((t) => `<p class="chart-note">${t}</p>`).join("") +
    chartTable(["Tranche", opts.unit[0].toUpperCase() + opts.unit.slice(1)], rows);
}

// Histogrammes de sécurité, fiscalité et prix de la dernière recherche.
function renderZoneCharts() {
  const communes = (lastData && lastData.communes) || [];
  const f = lastFiltres || {};
  const fmtEur = (v) => `${v.toLocaleString("fr-FR")} €`;

  $("#secu-deps").innerHTML = departementsRankHtml(communes);
  renderHistogram($("#secu-chart"), communes.map((c) => c.score_departement ?? null), {
    lo: 0,
    hi: 100,
    step: 10,
    tickEvery: 2,
    fmt: (v) => String(v),
    unit: "commune(s)",
    color: SECURITY_CLASSES[2].color,
    outside: f.securite_min != null ? (a, b) => b <= f.securite_min : null,
    filtre: f.securite_min != null ? `score départemental ≥ ${f.securite_min}` : "",
  });

  renderHistogram($("#fisc-chart"), communes.map((c) => (c.fiscalite ? c.fiscalite.taux : null)), {
    steps: [0.5, 1, 2, 5, 10],
    fmt: fmtTaux,
    unit: "commune(s)",
    color: FISC_COLORS[2],
    outside: f.fiscalite_max != null ? (a) => a > f.fiscalite_max : null,
    filtre: f.fiscalite_max != null ? `≤ ${fmtTaux(f.fiscalite_max)}` : "",
  });

  const quartiers = (immoData && immoData.quartiers) || [];
  const pmin = f.prix_m2_min;
  const pmax = f.prix_m2_max;
  const prixFiltre = pmin != null || pmax != null;
  renderHistogram($("#prix-chart"), quartiers.map((q) => (q.prix ? q.prix.prix_m2 : null)), {
    steps: [100, 250, 500, 1000, 2000, 5000, 10000],
    fmt: fmtEur,
    unit: "quartier(s)",
    color: IMMO_COLORS[2],
    outside: prixFiltre ? (a, b) => (pmin != null && b <= pmin) || (pmax != null && a > pmax) : null,
    filtre: !prixFiltre ? ""
      : pmin != null && pmax != null ? `${fmtEur(pmin)} – ${fmtEur(pmax)} /m²`
      : pmin != null ? `≥ ${fmtEur(pmin)} /m²`
      : `≤ ${fmtEur(pmax)} /m²`,
  });
}

function secteurClass(secteur) {
  const s = String(secteur || "").toLowerCase();
  if (s.includes("hors")) return "badge-hc";
  if (s.includes("priv")) return "badge-prive";
  return "badge-public";
}

// highlight : marque les établissements retenus / écartés par la sélection
// de l'histogramme des taux ; extra(e) : ligne de détail supplémentaire (HTML) ;
// empty : message d'une liste vide.
function renderEtabList(ul, etabs, { withRate = true, highlight = false, extra = null, empty = "Aucun établissement." } = {}) {
  if (!etabs.length) {
    ul.innerHTML = `<li class="empty">${empty}</li>`;
    return;
  }
  ul.innerHTML = "";
  for (const e of etabs) {
    const li = document.createElement("li");
    li.className = "etab-item";
    if (highlight) li.classList.add(inRateSel(e) ? "selected" : "dimmed");
    li.tabIndex = 0;
    const rate = successRate(e);
    const sub = [e.nature, (e.filieres || []).join(", "), e.commune].filter(Boolean).join(" · ");
    li.innerHTML =
      `<div class="etab-main"><div class="etab-name">${esc(e.nom)}</div>` +
      `<div class="etab-sub">${esc(sub)}</div>` +
      (extra ? `<div class="etab-sub etab-access">${extra(e)}</div>` : "") +
      `</div>` +
      `<div class="etab-side">` +
      (withRate && rate != null ? `<span class="etab-rate">${rate.toFixed(0)} %</span>` : "") +
      (e.secteur ? `<span class="badge ${secteurClass(e.secteur)}">${esc(e.secteur)}</span>` : "") +
      `</div>`;
    const open = () => {
      const m = markerByEtab.get(e) || sourceMarkerByUai.get(e.UAI);
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

// ---------------------------------------------------------------------------
// Arbre des quartiers : tous les quartiers IRIS de la zone, triés par commune
// puis par nom ; déplier un nœud sélectionne le quartier (voir openQuartier).
// ---------------------------------------------------------------------------
// Établissements accessibles depuis le quartier déplié : [{ e, criteres:
// [{ nature, dist, transp }] }] (voir reachableSources).
let openReachable = [];

// Emprise [minLon, minLat, maxLon, maxLat] d'une géométrie GeoJSON, ou d'un
// tableau de positions [lon, lat] (préfiltre des tests de recouvrement).
function geomBBox(geom) {
  const b = [Infinity, Infinity, -Infinity, -Infinity];
  const visit = (c) => {
    if (typeof c[0] === "number") {
      b[0] = Math.min(b[0], c[0]);
      b[1] = Math.min(b[1], c[1]);
      b[2] = Math.max(b[2], c[0]);
      b[3] = Math.max(b[3], c[1]);
    } else c.forEach(visit);
  };
  const coords = Array.isArray(geom) ? geom : geom && geom.coordinates;
  if (coords) visit(coords);
  return b;
}

// Indique si l'anneau ring ([[lon, lat], …]) recouvre une partie du contour
// GeoJSON : un sommet de l'un dans l'autre (les recouvrements sans sommet
// intérieur, rares à ces échelles, sont ignorés).
function ringTouchesGeometry(ring, geom) {
  const a = geomBBox(ring);
  const b = geomBBox(geom);
  if (a[0] > b[2] || b[0] > a[2] || a[1] > b[3] || b[1] > a[3]) return false;
  let touches = false;
  const visit = (c) => {
    if (touches) return;
    if (typeof c[0] === "number") touches = pointInRing(c[0], c[1], ring);
    else c.forEach(visit);
  };
  visit(geom.coordinates);
  return touches || ring.some(([lon, lat]) => pointInGeometry(lon, lat, geom));
}

// Établissements accessibles depuis un quartier : les sources dont l'isochrone
// (temps et mode de transport de leur critère) recouvre le quartier, une
// entrée par établissement (une école primaire peut l'être pour deux
// critères), triées par nature puis par taux de réussite décroissant.
function reachableSources(quartier) {
  const byUai = new Map();
  for (const src of (lastData && lastData.sources) || []) {
    if (!src.isochrone || src.isochrone.length < 3 || !ringTouchesGeometry(src.isochrone, quartier.contour)) continue;
    if (!byUai.has(src.UAI)) byUai.set(src.UAI, { e: src, criteres: [] });
    byUai.get(src.UAI).criteres.push({ nature: sourceNature(src), dist: src.dist, transp: src.transp });
  }
  const rank = (x) => {
    const i = NATURE_GROUPS.indexOf(natureGroup(x.criteres[0].nature));
    return i < 0 ? NATURE_GROUPS.length : i;
  };
  return [...byUai.values()].sort(
    (a, b) => rank(a) - rank(b) || (successRate(b.e) ?? -1) - (successRate(a.e) ?? -1),
  );
}

function renderQuartierTree() {
  const ul = $("#quartier-tree");
  openIris = null; // nœuds régénérés : aucun déplié (sélection déjà effacée par render)
  const quartiers = lastData ? zoneQuartiers() : [];
  openReachable = [];
  $("#quartier-count").textContent = quartiers.length ? `(${quartiers.length})` : "";
  if (!quartiers.length) {
    ul.innerHTML = `<li class="empty">${lastData ? "Aucun quartier dans la zone." : "Lancez une recherche pour afficher les quartiers."}</li>`;
    return;
  }
  const communeNom = new Map(((lastData && lastData.communes) || []).map((c) => [c.code_commune, c.nom]));
  const nomCommune = (q) => communeNom.get(q.code_commune) || "";
  const sorted = [...quartiers].sort(
    (a, b) => nomCommune(a).localeCompare(nomCommune(b), "fr") || a.nom.localeCompare(b.nom, "fr"),
  );
  ul.innerHTML =
    sorted
      .map((q) => {
        const prix = q.prix ? `<span class="qt-prix">${q.prix.prix_m2.toLocaleString("fr-FR")} €/m²</span>` : "";
        return (
          `<li class="qt-node" data-iris="${esc(q.iris)}">` +
          `<button type="button" class="qt-head" aria-expanded="false">` +
          `<span class="qt-name">${esc(q.nom)}<span class="qt-commune">${esc(nomCommune(q))}</span></span>` +
          `<span class="qt-meta">${prix}</span></button>` +
          `<div class="qt-body"></div></li>`
        );
      })
      .join("");

  // Quartier lu dans l'URL : déplié avec les premiers résultats.
  if (pendingQuartier) {
    const q = quartiers.find((x) => x.iris === pendingQuartier);
    pendingQuartier = null;
    if (q) openQuartier(q);
  }
}

// (Re)rend la liste des établissements accessibles depuis le quartier déplié
// (mise en avant de la sélection de l'histogramme des taux).
function renderQuartierEtabs() {
  if (openIris == null) return;
  const node = quartierNode(openIris);
  const ul = node && node.querySelector(".qt-etabs .etab-list");
  if (!ul) return;
  const access = new Map(openReachable.map((x) => [x.e, x.criteres]));
  renderEtabList(ul, openReachable.map((x) => x.e), {
    highlight: rateSel.size > 0,
    empty: "Aucun établissement source accessible depuis ce quartier.",
    extra: (e) =>
      "Accessible en " +
      access.get(e)
        .map((c) => `${Math.round(c.dist / 60)} min ${TRANSP_LABELS[c.transp] || esc(c.transp)} (${natureLabel(c.nature)})`)
        .join(", "),
  });
}

// En-tête d'un nœud : déplie (sélectionne et centre la carte sur le quartier)
// ou replie (désélectionne).
$("#quartier-tree").addEventListener("click", (ev) => {
  const head = ev.target.closest(".qt-head");
  if (!head) return;
  const iris = head.parentElement.dataset.iris;
  if (iris === openIris) {
    clearQuartier();
    return;
  }
  const q = zoneQuartiers().find((x) => x.iris === iris);
  if (!q) return;
  openQuartier(q);
  const bounds = L.geoJSON(q.contour).getBounds();
  if (bounds.isValid()) map.fitBounds(bounds, { padding: [40, 40], maxZoom: 15 });
});

renderInsights([]);
renderZoneCharts();

// ---------------------------------------------------------------------------
// Lien de vérification d'e-mail
// ---------------------------------------------------------------------------
const VERIFY_FAILED_MSG =
  "Ce lien de vérification est invalide ou a expiré. Connectez-vous pour en recevoir un nouveau.";

// Traite un lien /verify-email. Si un autre utilisateur est connecté dans ce
// navigateur, demande s'il faut le déconnecter avant d'activer le compte visé ;
// en cas de refus, le jeton n'est pas consommé (le lien reste utilisable).
// Renvoie "done" (application démarrée), "pending" (compte en attente),
// "failed" (lien invalide) ou "cancelled" (activation refusée).
async function handleVerifyLink(verifyToken) {
  const body = JSON.stringify({ token: verifyToken });
  if (token) {
    let current = null;
    try {
      current = await api("/user");
    } catch (_) {
      setToken(null);
    }
    if (current) {
      let target;
      try {
        target = await api("/auth/verify/info", { method: "POST", body });
      } catch (_) {
        return "failed";
      }
      if (target.user_id !== current.id) {
        const who = current.email || current.login || "un autre utilisateur";
        const ok = confirm(
          `Vous êtes connecté en tant que ${who}.\n\n` +
            `Se déconnecter pour activer le compte ${target.email || ""} ?`
        );
        if (!ok) return "cancelled";
        try {
          await api("/auth/logout", { method: "POST" });
        } catch (_) {
          /* token déjà invalide ou serveur injoignable */
        }
        setToken(null);
      }
    }
  }

  let data;
  try {
    data = await api("/auth/verify", { method: "POST", body });
  } catch (_) {
    return "failed";
  }
  if (!data.token) return "pending";
  // Compte autorisé : connexion directe.
  setToken(data.token);
  startApp(data.user);
  if (data.email_changed) {
    $("#user-chip").click();
    settingsMsg($("#email-form"), `Adresse e-mail confirmée : ${data.user.email}.`);
  }
  return "done";
}

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
      $("#oidc-login").addEventListener("click", saveReturnQuery);
    })
    .catch(() => {});

  // Retour du flux OIDC : token dans le fragment (#oidc_token=...) ou erreur
  // dans la requête (?oidc_error=...). L'URL est nettoyée dans les deux cas.
  const oidcToken = new URLSearchParams(location.hash.slice(1)).get("oidc_token");
  const oidcError = new URLSearchParams(location.search).get("oidc_error");
  if (oidcToken || oidcError) history.replaceState(null, "", "/");
  // Retour de la page de paiement Mollie : /?billing_return=1.
  const billingReturn = new URLSearchParams(location.search).has("billing_return");
  if (billingReturn) history.replaceState(null, "", "/");
  if (oidcToken) setToken(oidcToken);
  // Retour de SSO / paiement sur « / » : état de la page mis de côté au départ.
  const savedQuery = sessionStorage.getItem(RETURN_QUERY_KEY);
  sessionStorage.removeItem(RETURN_QUERY_KEY);
  if (savedQuery && (oidcToken || oidcError || billingReturn) && !location.search) {
    history.replaceState(null, "", `/${savedQuery}`);
  }

  // Lien de vérification reçu par e-mail : /verify-email?token=...
  if (location.pathname === "/verify-email") {
    const verifyToken = new URLSearchParams(location.search).get("token");
    // Retire le jeton de l'URL (historique, partage d'écran…).
    history.replaceState(null, "", "/");
    if (verifyToken) {
      const outcome = await handleVerifyLink(verifyToken);
      if (outcome === "done") return;
      if (outcome === "pending") verifiedPending = true;
      else if (outcome === "failed") verifyFailed = true;
    }
  }

  if (token) {
    try {
      const user = await api("/user");
      startApp(user);
      if (billingReturn) handleBillingReturn();
      if (verifyFailed) alert(VERIFY_FAILED_MSG);
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
    showInfo(VERIFY_FAILED_MSG, false);
  }
})();
