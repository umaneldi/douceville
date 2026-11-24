# Paquet `mail`

Envoi des e-mails transactionnels en texte brut : vérification d'adresse,
confirmation de changement d'e-mail, avertissement à l'ancienne adresse,
notification d'approbation d'un compte. Les textes des messages sont rédigés
dans `internal/api` ; ce paquet ne fait que les transporter.

## Fichiers

| Fichier | Contenu |
| ------- | ------- |
| `mail.go` | Interface `Sender` ; `SMTPSender` (envoi SMTP) ; `LogSender` (journalise l'e-mail au niveau `WARN` sans l'envoyer). |

## Fonctionnement

- `cmd/server` utilise `SMTPSender` si `SMTP_HOST` est défini, sinon
  `LogSender` (pratique en développement : le lien de vérification apparaît
  dans les journaux).
- Port `465` : TLS implicite ; autres ports : STARTTLS si le serveur le
  propose.
- Authentification si `SMTP_USERNAME` est défini, en choisissant parmi les
  mécanismes annoncés par le serveur : `PLAIN`, puis `LOGIN` (implémenté ici,
  absent de `net/smtp`). Les identifiants ne sont jamais envoyés en clair hors
  TLS, sauf vers `localhost`.
- Sujet encodé en MIME (UTF-8) ; l'adresse d'enveloppe est extraite de
  `SMTP_FROM` (`Nom <adresse>`).

## Dépendances

Bibliothèque standard uniquement (`net/smtp`, `crypto/tls`, `mime`).

## Points d'attention

- Un destinataire contenant `\r` ou `\n` est refusé (injection d'en-têtes).
- Délai par défaut de 30 s par envoi si le contexte n'a pas d'échéance.

## Tests

`go test ./internal/mail/` — choix du mécanisme d'authentification et
mécanisme `LOGIN` (refus hors TLS) (`mail_test.go`) ; envoi complet via
`SMTPSender` contre un serveur SMTP factice en mémoire (`fakeSMTP`,
`smtp_test.go`) : authentification `PLAIN`, enveloppe, en-têtes et encodage du
sujet, erreurs (pas d'`AUTH`, mécanisme inconnu, destinataire refusé, serveur
injoignable, injection d'en-têtes) ; `LogSender`.
