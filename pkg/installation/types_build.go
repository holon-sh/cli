package installation

func stageTypes(p *preparedTypes) error { return p.prepared.WriteClients(p.name, p.root) }
