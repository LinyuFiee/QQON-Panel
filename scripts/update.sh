#!/bin/bash -p
# Fixed, root-managed entry point; all arguments are validated by install.sh.
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
unset BASH_ENV ENV PYTHONPATH PYTHONHOME
exec /bin/bash -p /usr/local/lib/server-panel/install.sh --update "$@"
