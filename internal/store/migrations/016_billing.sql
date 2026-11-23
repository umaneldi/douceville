-- Facturation prépayée : chaque compte dispose d'un solde (en centimes),
-- débité à chaque requête payante et crédité par paiement (Mollie) ou par un
-- administrateur. Tous les montants sont des entiers en centimes.

ALTER TABLE utilisateur ADD COLUMN IF NOT EXISTS balance_cents BIGINT NOT NULL DEFAULT 0;
DO $$ BEGIN
    ALTER TABLE utilisateur ADD CONSTRAINT utilisateur_balance_non_negative CHECK (balance_cents >= 0);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Paramètres de facturation (ligne unique), modifiables par un administrateur.
CREATE TABLE IF NOT EXISTS billing_settings (
    id                     BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    price_per_origin_cents BIGINT NOT NULL DEFAULT 5  CHECK (price_per_origin_cents >= 0),
    isochrone_price_cents  BIGINT NOT NULL DEFAULT 2  CHECK (isochrone_price_cents >= 0),
    welcome_credit_cents   BIGINT NOT NULL DEFAULT 100 CHECK (welcome_credit_cents >= 0),
    min_topup_cents        BIGINT NOT NULL DEFAULT 500 CHECK (min_topup_cents >= 100),
    currency               TEXT   NOT NULL DEFAULT 'EUR',
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by             TEXT REFERENCES utilisateur(id) ON DELETE SET NULL
);
INSERT INTO billing_settings (id) VALUES (TRUE) ON CONFLICT (id) DO NOTHING;

-- Journal des mouvements de crédit ; utilisateur.balance_cents en est la somme,
-- tenue à jour dans la même transaction.
--   kind : topup (paiement), usage (requête), refund (requête échouée),
--          admin_adjust (ajustement manuel), welcome (crédit de bienvenue).
CREATE TABLE IF NOT EXISTS credit_transaction (
    id           BIGSERIAL PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES utilisateur(id) ON DELETE CASCADE,
    amount_cents BIGINT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('topup', 'usage', 'refund', 'admin_adjust', 'welcome')),
    description  TEXT NOT NULL DEFAULT '',
    reference    TEXT,
    created_by   TEXT REFERENCES utilisateur(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS credit_transaction_user_created ON credit_transaction (user_id, created_at DESC);

-- Paiements de rechargement auprès du prestataire (identifiant Mollie tr_…).
CREATE TABLE IF NOT EXISTS payment (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES utilisateur(id) ON DELETE CASCADE,
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    currency     TEXT NOT NULL,
    status       TEXT NOT NULL,
    mode         TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS payment_user_id ON payment (user_id);
