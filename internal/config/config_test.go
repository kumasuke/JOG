package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// chdirTemp moves into an isolated working directory so Load() does not
// pick up the repository's actual config.yaml or the user's $HOME/.jog.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
	return dir
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 9000, cfg.Server.Port)
	assert.Equal(t, "0.0.0.0", cfg.Server.Address)
	assert.Equal(t, "./data", cfg.Storage.DataDir)
	assert.Equal(t, "./data/metadata.db", cfg.Storage.MetadataDB)
	assert.Equal(t, "minioadmin", cfg.Auth.AccessKey)
	assert.Equal(t, "minioadmin", cfg.Auth.SecretKey)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
}

func TestLoad_NoFileNoEnv_ReturnsDefaults(t *testing.T) {
	chdirTemp(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, DefaultConfig(), cfg)
}

func TestLoad_EnvOverridesDefaults(t *testing.T) {
	chdirTemp(t)

	t.Setenv("JOG_SERVER_PORT", "8080")
	t.Setenv("JOG_SERVER_ADDRESS", "127.0.0.1")
	t.Setenv("JOG_STORAGE_DATA_DIR", "/var/jog/data")
	t.Setenv("JOG_STORAGE_METADATA_DB", "/var/jog/meta.db")
	t.Setenv("JOG_AUTH_ACCESS_KEY", "admin")
	t.Setenv("JOG_AUTH_SECRET_KEY", "secret")
	t.Setenv("JOG_LOGGING_LEVEL", "debug")
	t.Setenv("JOG_LOGGING_FORMAT", "console")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "127.0.0.1", cfg.Server.Address)
	assert.Equal(t, "/var/jog/data", cfg.Storage.DataDir)
	assert.Equal(t, "/var/jog/meta.db", cfg.Storage.MetadataDB)
	assert.Equal(t, "admin", cfg.Auth.AccessKey)
	assert.Equal(t, "secret", cfg.Auth.SecretKey)
	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "console", cfg.Logging.Format)
}

func TestLoad_InvalidPortEnv_ReturnsError(t *testing.T) {
	chdirTemp(t)
	t.Setenv("JOG_SERVER_PORT", "not-a-number")

	_, err := Load()
	require.Error(t, err)
}

// Empty env vars must be treated as "unset" so that environments which
// export JOG_* keys with empty defaults (CI matrices, container
// orchestrators, shell scripts that pre-declare every var) don't clobber
// file/default values. This matches viper's pre-rewrite behavior.
func TestLoad_EmptyEnv_TreatedAsUnset(t *testing.T) {
	dir := chdirTemp(t)

	// File sets non-default values for every key.
	yaml := []byte(`
server:
  port: 7777
  address: 192.168.0.1
storage:
  data_dir: /srv/data
auth:
  access_key: file-ak
logging:
  level: warn
`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644))

	// All env vars set to empty — these must not override the file.
	t.Setenv("JOG_SERVER_PORT", "")
	t.Setenv("JOG_SERVER_ADDRESS", "")
	t.Setenv("JOG_STORAGE_DATA_DIR", "")
	t.Setenv("JOG_AUTH_ACCESS_KEY", "")
	t.Setenv("JOG_LOGGING_LEVEL", "")

	cfg, err := Load()
	require.NoError(t, err) // empty JOG_SERVER_PORT must NOT cause Atoi failure
	assert.Equal(t, 7777, cfg.Server.Port)
	assert.Equal(t, "192.168.0.1", cfg.Server.Address)
	assert.Equal(t, "/srv/data", cfg.Storage.DataDir)
	assert.Equal(t, "file-ak", cfg.Auth.AccessKey)
	assert.Equal(t, "warn", cfg.Logging.Level)
}

func TestLoad_FileOverridesDefaults(t *testing.T) {
	dir := chdirTemp(t)

	yaml := []byte(`
server:
  port: 7777
  address: 192.168.0.1
storage:
  data_dir: /srv/data
  metadata_db: /srv/meta.db
auth:
  access_key: ak
  secret_key: sk
logging:
  level: warn
  format: console
`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644))

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 7777, cfg.Server.Port)
	assert.Equal(t, "192.168.0.1", cfg.Server.Address)
	assert.Equal(t, "/srv/data", cfg.Storage.DataDir)
	assert.Equal(t, "/srv/meta.db", cfg.Storage.MetadataDB)
	assert.Equal(t, "ak", cfg.Auth.AccessKey)
	assert.Equal(t, "sk", cfg.Auth.SecretKey)
	assert.Equal(t, "warn", cfg.Logging.Level)
	assert.Equal(t, "console", cfg.Logging.Format)
}

func TestLoad_PartialFile_KeepsDefaultsForUnsetKeys(t *testing.T) {
	dir := chdirTemp(t)

	// Only override server.port; everything else should keep defaults.
	yaml := []byte(`
server:
  port: 7777
`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644))

	cfg, err := Load()
	require.NoError(t, err)

	def := DefaultConfig()
	assert.Equal(t, 7777, cfg.Server.Port)
	assert.Equal(t, def.Server.Address, cfg.Server.Address)
	assert.Equal(t, def.Storage, cfg.Storage)
	assert.Equal(t, def.Auth, cfg.Auth)
	assert.Equal(t, def.Logging, cfg.Logging)
}

func TestLoad_EnvWinsOverFile(t *testing.T) {
	dir := chdirTemp(t)

	yaml := []byte(`
server:
  port: 7777
  address: 192.168.0.1
auth:
  access_key: file-ak
`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644))

	t.Setenv("JOG_SERVER_PORT", "8888")
	t.Setenv("JOG_AUTH_ACCESS_KEY", "env-ak")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 8888, cfg.Server.Port)             // env wins
	assert.Equal(t, "192.168.0.1", cfg.Server.Address) // file wins over default
	assert.Equal(t, "env-ak", cfg.Auth.AccessKey)      // env wins over file
}

func TestLoad_MalformedYAML_ReturnsError(t *testing.T) {
	dir := chdirTemp(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server: : :\n"), 0o644))

	_, err := Load()
	require.Error(t, err)
}

func TestLoad_HomeDirConfig(t *testing.T) {
	chdirTemp(t)
	home := os.Getenv("HOME")

	jogDir := filepath.Join(home, ".jog")
	require.NoError(t, os.MkdirAll(jogDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(jogDir, "config.yaml"),
		[]byte("server:\n  port: 5555\n"), 0o644))

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 5555, cfg.Server.Port)
}

func TestLoad_CwdBeatsHomeDir(t *testing.T) {
	dir := chdirTemp(t)
	home := os.Getenv("HOME")

	jogDir := filepath.Join(home, ".jog")
	require.NoError(t, os.MkdirAll(jogDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(jogDir, "config.yaml"),
		[]byte("server:\n  port: 5555\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("server:\n  port: 6666\n"), 0o644))

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 6666, cfg.Server.Port, "cwd config.yaml should take precedence over $HOME/.jog/config.yaml")
}

func TestLoadFromFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "myconf.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
server:
  port: 4242
`), 0o644))

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, 4242, cfg.Server.Port)
	// Defaults preserved for unset fields.
	assert.Equal(t, "0.0.0.0", cfg.Server.Address)
	assert.Equal(t, "minioadmin", cfg.Auth.AccessKey)
}

// TestLoad_OldEnvNames_NotReflected verifies that legacy env var names
// (JOG_ACCESS_KEY, JOG_PORT, etc.) have no effect on the loaded config.
// This is the negative test for the env-name migration (CR-1).
func TestLoad_OldEnvNames_NotReflected(t *testing.T) {
	chdirTemp(t)

	t.Setenv("JOG_ACCESS_KEY", "should-not-apply")
	t.Setenv("JOG_SECRET_KEY", "should-not-apply")
	t.Setenv("JOG_PORT", "1234")
	t.Setenv("JOG_DATA_DIR", "/should/not/apply")
	t.Setenv("JOG_LOG_LEVEL", "debug")

	cfg, err := Load()
	require.NoError(t, err)

	def := DefaultConfig()
	assert.Equal(t, def.Auth.AccessKey, cfg.Auth.AccessKey, "legacy JOG_ACCESS_KEY must not override auth.access_key")
	assert.Equal(t, def.Auth.SecretKey, cfg.Auth.SecretKey, "legacy JOG_SECRET_KEY must not override auth.secret_key")
	assert.Equal(t, def.Server.Port, cfg.Server.Port, "legacy JOG_PORT must not override server.port")
	assert.Equal(t, def.Storage.DataDir, cfg.Storage.DataDir, "legacy JOG_DATA_DIR must not override storage.data_dir")
	assert.Equal(t, def.Logging.Level, cfg.Logging.Level, "legacy JOG_LOG_LEVEL must not override logging.level")
}

func TestLoadFromFile_NotFound_ReturnsError(t *testing.T) {
	_, err := LoadFromFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err)
}

func TestLoadFromFile_MalformedYAML_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server: : :\n"), 0o644))

	_, err := LoadFromFile(path)
	require.Error(t, err)
}

// ----- LifecycleConfig tests -----

func TestDefaultConfig_LifecycleDefaults(t *testing.T) {
	cfg := DefaultConfig()
	assert.True(t, cfg.Lifecycle.Enabled)
	assert.Equal(t, Duration(time.Hour), cfg.Lifecycle.Interval)
	assert.Equal(t, 10000, cfg.Lifecycle.MaxActionsPerCycle)
}

func TestLifecycleConfig_YAMLUnmarshal(t *testing.T) {
	raw := []byte(`
lifecycle:
  enabled: false
  interval: 30m
  max_actions_per_cycle: 500
`)
	cfg := DefaultConfig()
	require.NoError(t, yaml.Unmarshal(raw, cfg))
	assert.False(t, cfg.Lifecycle.Enabled)
	assert.Equal(t, Duration(30*time.Minute), cfg.Lifecycle.Interval)
	assert.Equal(t, 500, cfg.Lifecycle.MaxActionsPerCycle)
}

func TestLifecycleConfig_EnvOverlay(t *testing.T) {
	chdirTemp(t)
	t.Setenv("JOG_LIFECYCLE_ENABLED", "false")
	t.Setenv("JOG_LIFECYCLE_INTERVAL", "2h")
	t.Setenv("JOG_LIFECYCLE_MAX_ACTIONS", "50")

	cfg, err := Load()
	require.NoError(t, err)
	assert.False(t, cfg.Lifecycle.Enabled)
	assert.Equal(t, Duration(2*time.Hour), cfg.Lifecycle.Interval)
	assert.Equal(t, 50, cfg.Lifecycle.MaxActionsPerCycle)
}

func TestLifecycleConfig_InvalidDurationEnv_ReturnsError(t *testing.T) {
	chdirTemp(t)
	t.Setenv("JOG_LIFECYCLE_INTERVAL", "not-a-duration")

	_, err := Load()
	require.Error(t, err)
}

func TestLifecycleConfig_InvalidBoolEnv_ReturnsError(t *testing.T) {
	chdirTemp(t)
	t.Setenv("JOG_LIFECYCLE_ENABLED", "not-a-bool")

	_, err := Load()
	require.Error(t, err)
}

// ----- NotificationConfig tests -----

func TestDefaultConfig_NotificationDefaults(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, "us-east-1", cfg.Notification.Region)
	assert.Equal(t, Duration(10*time.Second), cfg.Notification.DeliveryTimeout)
	assert.False(t, cfg.Notification.BlockPrivateTargets)
	assert.NotNil(t, cfg.Notification.Targets)
	assert.Empty(t, cfg.Notification.Targets)
}

func TestNotificationConfig_YAMLUnmarshal(t *testing.T) {
	raw := []byte(`
notification:
  region: eu-west-1
  delivery_timeout: 3s
  block_private_targets: true
  targets:
    "arn:aws:sns:eu-west-1:000000000000:hook": https://example.test/webhook
    "arn:aws:sqs:eu-west-1:000000000000:q": https://example.test/queue
`)
	cfg := DefaultConfig()
	require.NoError(t, yaml.Unmarshal(raw, cfg))
	assert.Equal(t, "eu-west-1", cfg.Notification.Region)
	assert.Equal(t, Duration(3*time.Second), cfg.Notification.DeliveryTimeout)
	assert.True(t, cfg.Notification.BlockPrivateTargets)
	assert.Equal(t, map[string]string{
		"arn:aws:sns:eu-west-1:000000000000:hook": "https://example.test/webhook",
		"arn:aws:sqs:eu-west-1:000000000000:q":    "https://example.test/queue",
	}, cfg.Notification.Targets)
}

func TestNotificationConfig_EnvOverlay(t *testing.T) {
	chdirTemp(t)
	t.Setenv("JOG_NOTIFICATION_REGION", "ap-northeast-1")
	t.Setenv("JOG_NOTIFICATION_DELIVERY_TIMEOUT", "15s")
	t.Setenv("JOG_NOTIFICATION_BLOCK_PRIVATE_TARGETS", "true")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "ap-northeast-1", cfg.Notification.Region)
	assert.Equal(t, Duration(15*time.Second), cfg.Notification.DeliveryTimeout)
	assert.True(t, cfg.Notification.BlockPrivateTargets)
}

func TestNotificationConfig_InvalidDurationEnv_ReturnsError(t *testing.T) {
	chdirTemp(t)
	t.Setenv("JOG_NOTIFICATION_DELIVERY_TIMEOUT", "not-a-duration")

	_, err := Load()
	require.Error(t, err)
}

func TestNotificationConfig_InvalidBlockPrivateEnv_ReturnsError(t *testing.T) {
	chdirTemp(t)
	t.Setenv("JOG_NOTIFICATION_BLOCK_PRIVATE_TARGETS", "not-a-bool")

	_, err := Load()
	require.Error(t, err)
}

func TestLoadFromFile_EnvDoesNotOverride(t *testing.T) {
	// LoadFromFile is the explicit "use this file" path; the historical
	// behavior (viper SetConfigFile + Unmarshal) did not consult env vars,
	// and we preserve that.
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  port: 1111\n"), 0o644))

	t.Setenv("JOG_SERVER_PORT", "2222")
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, 1111, cfg.Server.Port)
}
