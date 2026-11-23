-- Changement d'adresse e-mail : un jeton de vérification portant new_email
-- confirme la nouvelle adresse, qui ne remplace l'ancienne qu'à la vérification.
-- Les jetons sans new_email restent les jetons de vérification d'inscription.
ALTER TABLE email_verification ADD COLUMN IF NOT EXISTS new_email TEXT;
