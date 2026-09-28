package service

// keyCol returns the properly quoted 'key' column name. "key" is a reserved
// word in both MySQL and PostgreSQL, so raw SQL must quote it per dialect.
func keyCol(isPG bool) string {
	if isPG {
		return `"key"`
	}
	return "`key`"
}
