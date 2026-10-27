/* Our settings over dropbear's default_options.h. Sessions get <cfw>/bin on PATH, so the server
 * side of scp (a link to dropbearmulti there) is found; <cfw> is /bkupgrade/cfw (bergamot, orion)
 * or /data/cfw (pickle). */
#define DEFAULT_ROOT_PATH "/usr/sbin:/usr/bin:/sbin:/bin:/bkupgrade/cfw/bin:/data/cfw/bin"
#define DEFAULT_PATH "/usr/bin:/bin:/bkupgrade/cfw/bin:/data/cfw/bin"
