# Abnahme Teilprojekt 7 (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-14-mc-panel-ui-redesign-design.md` (§7).

Automatisch geprüft (CI und `make test-vm`):
- **Tokens:** Kontrast aller Text-/Flächenpaare in Light und Dark (AA), §-Farben auf Seite und Konsole (`web/src/styles/tokens.test.ts`).
- **Bausteine und Komponenten:** `StatSlot` („–“ statt 0, Balken geklemmt), `PingBars`, `PixelIcon`, `McText` (kein HTML), Varianten der shadcn-Komponenten.
- **Navigation:** `serverSections` für alle Rechte-Kombinationen, Weiterleitung unerlaubter Bereiche, Bereich bleibt beim Serverwechsel, Baum-Zustand (auch ohne `localStorage`), Tablet-Flyout, Mobil-Drawer, genau ein Löschformular in den Einstellungen.
- **Seiten:** Mehrspieler-Liste (alle bisherigen Prüfungen der Startseite), Konsolenhöhe, Blocker im Dateien-Bereich (wegnavigieren, andere Datei öffnen), Kachel-Raster der Plugins, Admin-Tabellen und Auth-Panel.
- **Playwright:** alle bisherigen Abläufe gegen Mock und echtes Backend, Navigation über den Server-Baum.

Manuell auf der Test-VM: echter Browser (Chrome/Firefox/Safari), Tastatur, echte Server und Gefühl.

## Vorbereitung

1. Deploy von `main`: `make deploy-testvm` (PRs #80–#82 gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>.
2. Als `admin` anmelden; mindestens ein laufender (`survival`) und ein gestoppter Server existieren, Benutzer `freund` hat nur `server.view` auf `survival`.

## Checkliste

- [ ] **Look:** Light und Dark (Theme-Umschalter unten in der Icon-Leiste) sehen auf allen Seiten nach dem neuen Design aus; nirgends mehr neutrales shadcn-Grau, Pixel-Überschriften, Block-Buttons, Slots. Konsole ist in beiden Modi dunkel.
- [ ] **Startseite:** Mehrspieler-Liste mit Icon, Adresse (kopierbar), Spielerzahl, Ping-Balken, MOTD-Zeile; Power-Buttons als Icons mit Tooltip; Stopp fragt nach. Admin sieht Host-Slots.
- [ ] **Navigation:** Server-Baum zeigt alle Server mit Status-Punkt, der aktive ist aufgeklappt; ein Klick wechselt Server und behält den Bereich (z. B. Konsole → Konsole). Baum einklappen bleibt nach Neuladen eingeklappt.
- [ ] **URLs:** `/servers/<id>/console` direkt öffnen und neu laden → bleibt in der Konsole. Als `freund` `/servers/<id>/files` öffnen → landet auf der Übersicht.
- [ ] **Server-Kopf:** Status, Adresse, Power und Slots (Spieler, TPS, RAM, CPU) in jedem Bereich; „Neustart nötig“ erscheint nach einer Einstellungsänderung.
- [ ] **Dateien:** ab breitem Fenster Liste und Editor nebeneinander; mit ungespeicherter Änderung im Editor auf einen anderen Server/Bereich klicken → Nachfrage; „Abbrechen“ bleibt, „Verwerfen“ geht.
- [ ] **Einstellungen:** Gefahrenzone „Server löschen“ am Ende, mit Bestätigung und Sudo.
- [ ] **Mobil** (Handy oder DevTools 390 px): Drawer mit Leiste und Baum, keine horizontale Scrollleiste (außer Tabellen/Konsole), Slots 2×2.
- [ ] **Tablet** (~900 px): nur Icon-Leiste, Server-Icon öffnet den Baum als Flyout.
- [ ] **Tastatur:** Tab durch Leiste, Baum und Seiten; Fokus überall sichtbar (grün).
- [ ] **Login/2FA:** zentriertes Panel mit Grasblock; Sudo-Dialog im neuen Stil.
- [ ] **CSP:** Browser-Konsole ohne CSP-Fehler; Schriften laden vom Panel selbst (Netzwerk-Tab, keine fremden Hosts).
