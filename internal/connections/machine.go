package connections

import "github.com/l4ci/rota/internal/cfgdir"

// Machine is the global config dir and the secret stores of this machine, from
// env: the one place the lookup order is wired. keychain yields the OS
// keychain; nil, or one that yields nil, means none here.
func Machine(env cfgdir.Env, keychain func() Store) (string, *Secrets, error) {
	dir, err := cfgdir.Dir(env)
	if err != nil {
		return "", nil, err
	}
	data, err := cfgdir.DataDir(env)
	if err != nil {
		return "", nil, err
	}
	sec := &Secrets{Getenv: env.Getenv, File: FileStore(data, dir)}
	if keychain != nil {
		if kc := keychain(); kc != nil {
			sec.Keychain = kc
		}
	}
	return dir, sec, nil
}
