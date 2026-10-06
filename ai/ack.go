package ai

// AckAddr: Anbieter und Rechner, an die Fragen und Anhänge gehen (für den Hinweis vor der ersten Nutzung, auth.AckAddr);
// "" = KI aus. Ändert der Admin den Anbieter oder die Adresse, muss der Hinweis neu bestätigt werden.
func (s *Svc) AckAddr() string {
	c := s.config()
	if !c.enabled() {
		return ""
	}
	out := c.Provider + " (" + hostOf(c) + ")"
	if a, ok := c.altConfig(); ok {
		out += ", " + a.Provider + " (" + hostOf(a) + ")"
	}
	return out
}
