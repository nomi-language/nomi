# Install `nomi` on Windows from a release, in PowerShell:
#
#	irm https://nomi-lang.org/install.ps1 | iex
#
# The site serves this file: scripts/build-tour-wasm.sh copies it to
# tour/public/install.ps1, which is not committed, so this file is the one
# source. scripts/install.sh installs on macOS and Linux and takes the same
# variables.
#
# It downloads the release's zip for Windows on amd64, checks its SHA-256
# against the checksums file the release publishes, and puts nomi.exe in
# %USERPROFILE%\.nomi\bin. On arm64 Windows it installs the amd64 build,
# which Windows runs under emulation; on 32-bit Windows it refuses. It needs
# no administrator and no change to the execution policy, and runs in
# Windows PowerShell 5.1 and PowerShell 7. When the directory is not on
# PATH, it adds it to the front of the user PATH in the registry (a rerun
# adds nothing) and to this window's PATH, so `nomi` works in the same
# window, and writes nothing else outside the directory.
#
# Run again, it replaces nomi.exe, a running one too: Windows cannot write
# over a running executable but can rename it, so the old file becomes
# nomi.exe.old, which the next nomi to start removes. `nomi upgrade` uses
# the same name (asideName and removeUpgradeAside in
# cmd/nomi/upgrade_cmd.go). It prints the new `nomi --version`, which names
# each editor whose Nomi support is from another version. Outside replace
# mode it rewrites no editor's files.
#
# When the window's PATH finds another nomi.exe before this one, it says
# which file that is, what it is and what removes it (Find-OtherNomi).
# Replace mode, NOMI_REPLACE=1, moves a machine with a build from source
# onto the release: after the install it removes each other nomi.exe on
# PATH that is a build from source, leaves and names any other, and has the
# new nomi.exe write the Nomi support of each editor that has any.
#
# Variables:
#
#	NOMI_VERSION           the release to install, such as v0.0.1; the
#	                       latest release when unset
#	NOMI_INSTALL_DIR       the directory nomi.exe goes in,
#	                       %USERPROFILE%\.nomi\bin when unset
#	NOMI_NO_MODIFY_PATH    1 changes no PATH and prints what to add
#	NOMI_REPLACE           1 is replace mode: it removes each other
#	                       nomi.exe on PATH built from source and sets up
#	                       the editors from this one
#	NOMI_INSTALL_BASE_URL  the releases root, in place of
#	                       https://github.com/nomi-language/nomi/releases;
#	                       for tests
#	NOMI_INSTALL_OS        stands in for [Environment]::OSVersion.Platform,
#	                       which is Win32NT on Windows; for tests
#	NOMI_INSTALL_ARCH      stands in for the processor architecture (AMD64,
#	                       ARM64 or x86); for tests
#	NOMI_INSTALL_USER_PATH_FILE
#	                       a file whose text stands in for the user PATH in
#	                       the registry, read and written in its place; for
#	                       tests only, so that no test changes the real one
#
# The whole script is one function, called on the last line, so a download
# cut short runs nothing. The file is ASCII, so every PowerShell reads it
# alike.

function Install-Nomi {
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue'
	$installPage = 'https://nomi-lang.org/tooling/install/'
	$nextCommand = 'nomi editor install <editor>    # helix, nvim, vscode or zed'

	function Fail([string]$message) {
		throw "install.ps1: $message"
	}

	# Test-PathHolds says whether the PATH list $list, split at $separator,
	# names the directory $want, as written or with its variables expanded,
	# with or without a trailing separator.
	function Test-PathHolds([string]$list, [string]$separator, [string]$want) {
		$wanted = $want.TrimEnd('\', '/')
		foreach ($entry in ($list -split [regex]::Escape($separator))) {
			$entry = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"')).TrimEnd('\', '/')
			if ($entry -and $entry -eq $wanted) {
				return $true
			}
		}
		return $false
	}

	# Get-UserPath is the user PATH as the registry holds it, its variables
	# unexpanded, so that writing it back changes no entry.
	function Get-UserPath {
		$file = $env:NOMI_INSTALL_USER_PATH_FILE
		if ($file) {
			if (Test-Path -LiteralPath $file) {
				return [IO.File]::ReadAllText($file)
			}
			return ''
		}
		$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
		if ($null -eq $key) {
			return ''
		}
		try {
			return [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
		} finally {
			$key.Close()
		}
	}

	# Set-UserPath writes the user PATH, keeping the kind of value the
	# registry held, so an entry such as %USERPROFILE%\bin still expands.
	function Set-UserPath([string]$value) {
		$file = $env:NOMI_INSTALL_USER_PATH_FILE
		if ($file) {
			[IO.File]::WriteAllText($file, $value)
			return
		}
		$key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
		try {
			$kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
			if ($key.GetValueNames() -contains 'Path') {
				$kind = $key.GetValueKind('Path')
			}
			$key.SetValue('Path', $value, $kind)
		} finally {
			$key.Close()
		}
		# Setting a user variable through .NET tells running programs that
		# the environment changed, so a terminal started from Explorer
		# afterwards has the new PATH. This one is set and removed for that.
		[Environment]::SetEnvironmentVariable('NOMI_INSTALL_PATH_CHANGED', '1', 'User')
		[Environment]::SetEnvironmentVariable('NOMI_INSTALL_PATH_CHANGED', [NullString]::Value, 'User')
	}

	# Resolve-Links is the file $path with its links followed.
	function Resolve-Links([string]$path) {
		for ($hop = 0; $hop -lt 40; $hop++) {
			$item = Get-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
			if ($null -eq $item -or -not ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
				break
			}
			$target = [string](@($item.Target)[0])
			if (-not $target) {
				break
			}
			if (-not [IO.Path]::IsPathRooted($target)) {
				$target = Join-Path (Split-Path -Parent $path) $target
			}
			$path = [IO.Path]::GetFullPath($target)
		}
		return $path
	}

	# Get-NomiKind says what the nomi.exe at $file is, $real being the file
	# with its links followed: in Is, in words in What, and in Removal what
	# removes one this script leaves. scripts/install.sh's nomi_kind reads
	# the same lines the same way.
	#
	#	homebrew  under a Homebrew Cellar
	#	release   its --version names a release and the commit it was cut
	#	          from, as a release archive's nomi does
	#	source    any other `nomi --version` line: built in a clone, which
	#	          names a commit alone, or with `go install ...@version`,
	#	          which names a version alone
	#	unknown   it does not run, or prints something else
	function Get-NomiKind([string]$file, [string]$real) {
		$kind = @{ Is = 'unknown'; What = 'which this script cannot identify'; Removal = 'Remove it yourself if it is not wanted.' }
		if (($real -split '[\\/]') -ccontains 'Cellar') {
			$kind.Is = 'homebrew'
			$kind.What = 'installed by Homebrew'
			$kind.Removal = 'To remove it, run: brew uninstall nomi'
			return $kind
		}
		$line = ''
		try {
			$lines = @(& $file --version 2>$null)
			if ($lines.Count -gt 0) {
				$line = [string]$lines[0]
			}
		} catch {
		}
		if ($line -cmatch '^nomi v[0-9].* \(.*, .*\)$') {
			$kind.Is = 'release'
			$kind.What = "a release installed another way ($line)"
		} elseif ($line -cmatch '^nomi .+ \(.*\)$') {
			$kind.Is = 'source'
			$kind.What = "a build from source ($line)"
		}
		return $kind
	}

	# Find-OtherNomi looks at each nomi.exe the PATH list $search finds
	# other than the one installed in $dir, a file once however many
	# entries reach it, its links followed. It looks only at a file named
	# nomi.exe in a directory of the list.
	#
	# Without $replace it changes nothing: when the first nomi.exe on the
	# list is another one, it says which file that is, what it is
	# (Get-NomiKind) and what removes it.
	#
	# With $replace it removes each one that is a build from source and
	# says so, and names each one it leaves: one Homebrew installed, a
	# release, and one it cannot identify. Then the installed nomi.exe
	# writes the Nomi support of each editor that has any (`nomi editor
	# install --installed --force`). A file it cannot remove, and an editor
	# command that fails, are each an error line, and the script goes on
	# and fails at its end.
	function Find-OtherNomi([string]$dir, [string]$search, [string]$separator, [bool]$replace) {
		$exe = Join-Path $dir 'nomi.exe'
		$ours = Resolve-Links $exe
		$seen = @()
		$failed = $false
		$said = $false
		foreach ($entry in ($search -split [regex]::Escape($separator))) {
			$entry = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"'))
			if (-not $entry -or -not [IO.Path]::IsPathRooted($entry)) {
				continue
			}
			$file = Join-Path $entry 'nomi.exe'
			if (-not (Test-Path -LiteralPath $file -PathType Leaf)) {
				continue
			}
			$real = Resolve-Links $file
			if ($seen -contains $real) {
				continue
			}
			$seen += $real
			if ($real -eq $ours) {
				if ($replace) {
					continue
				}
				return
			}
			$kind = Get-NomiKind $file $real
			if (-not $replace) {
				Write-Host ''
				Write-Host "Your PATH finds another nomi first: $file, $($kind.What)."
				if ($kind.Is -eq 'source') {
					Write-Host 'To remove it and move your editors to this release, run: $env:NOMI_REPLACE=1; irm https://nomi-lang.org/install.ps1 | iex'
				} elseif ($kind.Is -eq 'homebrew') {
					Write-Host $kind.Removal
				} else {
					Write-Host "Remove it yourself, or put $dir ahead of it on your PATH."
				}
				return
			}
			if (-not $said) {
				Write-Host ''
				$said = $true
			}
			if ($kind.Is -ne 'source') {
				Write-Host "Left $file, $($kind.What). $($kind.Removal)"
				continue
			}
			try {
				Remove-Item -LiteralPath $file -Force
				Write-Host "Removed $file, $($kind.What)"
			} catch {
				# Windows removes no executable that is running.
				[Console]::Error.WriteLine("install.ps1: could not remove $file, $($kind.What). If it is running, close the editor that started it and run this again.")
				$failed = $true
			}
		}
		if (-not $replace) {
			return
		}
		Write-Host ''
		$installed = $false
		try {
			& $exe editor install --installed --force | ForEach-Object { Write-Host $_ }
			$installed = $LASTEXITCODE -eq 0
		} catch {
		}
		if (-not $installed) {
			[Console]::Error.WriteLine("install.ps1: $exe editor install --installed --force failed. Set up each editor with: $exe editor install <editor>")
			$failed = $true
		}
		Write-Host ''
		Write-Host "A clone of the repository that an old nomi was built in can be deleted by hand. Nomi's caches clear themselves."
		Write-Host "If Neovim loads Nomi's plugin through a plugin manager, remove that entry from your Neovim configuration and run: nomi editor install nvim"
		if ($failed) {
			Fail 'replace mode did not finish: the lines above say which steps failed'
		}
	}

	$base = $env:NOMI_INSTALL_BASE_URL
	if (-not $base) {
		$base = 'https://github.com/nomi-language/nomi/releases'
	}
	$base = $base.TrimEnd('/')

	$os = $env:NOMI_INSTALL_OS
	if (-not $os) {
		$os = [string][Environment]::OSVersion.Platform
	}
	if ($os -ne 'Win32NT') {
		Fail "this script installs on Windows, and found $os. On macOS and Linux, run: curl -fsSL https://nomi-lang.org/install.sh | sh"
	}
	$arch = $env:NOMI_INSTALL_ARCH
	if (-not $arch) {
		# A 32-bit PowerShell on 64-bit Windows sees x86 here and the
		# machine's architecture in PROCESSOR_ARCHITEW6432.
		$arch = $env:PROCESSOR_ARCHITEW6432
		if (-not $arch) {
			$arch = $env:PROCESSOR_ARCHITECTURE
		}
	}
	switch ($arch) {
		'AMD64' { break }
		'ARM64' { Write-Host 'This is arm64 Windows: installing the amd64 build, which Windows runs under emulation.'; break }
		'x86' { Fail "there is no release of nomi for 32-bit Windows. $installPage lists the other ways to install it" }
		default { Fail "there is no release of nomi for the architecture $arch. $installPage lists the other ways to install it" }
	}

	$dir = $env:NOMI_INSTALL_DIR
	if (-not $dir) {
		if (-not $env:USERPROFILE) {
			Fail 'USERPROFILE is not set; set NOMI_INSTALL_DIR to the directory to install nomi in'
		}
		$dir = Join-Path (Join-Path $env:USERPROFILE '.nomi') 'bin'
	}
	$dir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($dir)

	if ($PSVersionTable.PSVersion.Major -lt 6) {
		# Windows PowerShell may offer only TLS 1.0 and 1.1, which GitHub
		# refuses.
		[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
	}

	$version = $env:NOMI_VERSION
	if (-not $version) {
		# The latest release answers with a redirect to its tag's page,
		# which names the version without a call to GitHub's API.
		$latest = "$base/latest"
		$location = $null
		try {
			$request = [Net.WebRequest]::Create($latest)
			$request.AllowAutoRedirect = $false
			$response = $request.GetResponse()
			$location = $response.Headers['Location']
			$response.Close()
		} catch {
			Fail "could not find the latest release at $latest"
		}
		if ($location -cmatch '/tag/([^/?#]+)$') {
			$version = $Matches[1]
		} else {
			Fail "found no release at $latest"
		}
	}
	if ($version -cmatch '^[0-9]') {
		$version = "v$version"
	}
	if ($version -cnotmatch '^v[0-9][0-9A-Za-z.+-]*$') {
		Fail "$version is not a release version such as v0.0.1"
	}

	$archive = "nomi_${version}_windows_amd64.zip"
	$sums = "nomi_${version}_checksums.txt"
	$exe = Join-Path $dir 'nomi.exe'

	try {
		New-Item -ItemType Directory -Force -Path $dir | Out-Null
	} catch {
		Fail "could not create $dir"
	}
	# The download goes in the install directory, so nothing is written
	# outside it and the last step is a rename within one file system.
	$work = Join-Path $dir ('.nomi-install.' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
	try {
		New-Item -ItemType Directory -Path $work | Out-Null
	} catch {
		Fail "could not create a directory in $dir"
	}
	try {
		Write-Host "Downloading nomi $version for windows/amd64"
		foreach ($name in @($archive, $sums)) {
			$url = "$base/download/$version/$name"
			try {
				Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile (Join-Path $work $name)
			} catch {
				Fail "could not download $url"
			}
		}

		$want = $null
		foreach ($line in [IO.File]::ReadAllLines((Join-Path $work $sums))) {
			$fields = -split $line
			if ($fields.Count -ge 2 -and ($fields[1] -replace '^\*', '' -replace '^\./', '') -ceq $archive) {
				$want = $fields[0].ToLowerInvariant()
				break
			}
		}
		if (-not $want) {
			Fail "$sums lists no $archive; nothing was installed"
		}
		$got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $work $archive)).Hash.ToLowerInvariant()
		if ($got -ne $want) {
			Fail "the SHA-256 of $archive is $got, and $sums says $want; nothing was installed"
		}

		$unpack = Join-Path $work 'unpack'
		try {
			Add-Type -AssemblyName System.IO.Compression.FileSystem
			[IO.Compression.ZipFile]::ExtractToDirectory((Join-Path $work $archive), $unpack)
		} catch {
			Fail "could not unpack $archive"
		}
		$new = Join-Path $unpack 'nomi.exe'
		if (-not (Test-Path -LiteralPath $new -PathType Leaf)) {
			Fail "$archive holds no nomi.exe; nothing was installed"
		}

		# The name `nomi upgrade` moves a running nomi.exe to and the next
		# nomi to start removes: asideName and removeUpgradeAside in
		# cmd/nomi/upgrade_cmd.go.
		$aside = "$exe.old"
		$replacing = Test-Path -LiteralPath $exe
		if ($replacing) {
			if (Test-Path -LiteralPath $aside) {
				try {
					Remove-Item -LiteralPath $aside -Force
				} catch {
					Fail "could not remove $aside, an earlier nomi.exe that is still running; close it and run this again"
				}
			}
			try {
				Move-Item -LiteralPath $exe -Destination $aside
			} catch {
				Fail "could not move the nomi.exe in $dir aside; nothing was installed"
			}
		}
		try {
			Move-Item -LiteralPath $new -Destination $exe
		} catch {
			if ($replacing) {
				Move-Item -LiteralPath $aside -Destination $exe -ErrorAction SilentlyContinue
			}
			Fail "could not move nomi.exe into $dir"
		}
		if ($replacing) {
			# A nomi.exe that is running cannot be removed, and stays until
			# a nomi starts.
			Remove-Item -LiteralPath $aside -Force -ErrorAction SilentlyContinue
		}
	} finally {
		Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
	}

	# `nomi --version` prints its line, then one per editor whose Nomi
	# support is from another version, with the command that rewrites it.
	$about = $null
	try {
		$lines = @(& $exe --version 2>$null)
		if ($LASTEXITCODE -eq 0 -and $lines.Count -gt 0) {
			$about = $lines
		}
	} catch {
	}
	if (-not $about) {
		$about = @("nomi $version")
	}
	Write-Host "Installed $($about[0]) at $exe"
	foreach ($line in ($about | Select-Object -Skip 1)) {
		Write-Host $line
	}

	# Replace mode sets up the editors itself, so it names no command to.
	$replace = $env:NOMI_REPLACE -eq '1'
	# The variable is read as PATH, the name every system gives it: Windows
	# takes any case, and PowerShell on Linux and macOS, where the tests also
	# run this script, has only that one.
	$separator = [string][IO.Path]::PathSeparator
	if (Test-PathHolds $env:PATH $separator $dir) {
		if (-not $replace) {
			Write-Host ''
			Write-Host 'Next, set up your editor:'
			Write-Host "  $nextCommand"
		}
		Find-OtherNomi $dir $env:PATH $separator $replace
		return
	}
	if ($env:NOMI_NO_MODIFY_PATH -eq '1' -or $dir.Contains(';')) {
		Write-Host ''
		Write-Host "$dir is not on your PATH. Add it to your user PATH (""Edit environment variables for your account"" in Settings), and run this in this window:"
		Write-Host "  `$env:Path = ""$dir$separator`$env:Path"""
		if (-not $replace) {
			Write-Host ''
			Write-Host 'Then set up your editor:'
			Write-Host "  $nextCommand"
		}
		# The PATH that line gives.
		Find-OtherNomi $dir "$dir$separator$env:PATH" $separator $replace
		return
	}
	$userPath = Get-UserPath
	if (-not (Test-PathHolds $userPath ';' $dir)) {
		if ($userPath) {
			Set-UserPath "$dir;$userPath"
		} else {
			Set-UserPath $dir
		}
		Write-Host "Added $dir to your user PATH, which terminals opened from now on have."
	}
	$env:PATH = "$dir$separator$env:PATH"
	if (-not $replace) {
		Write-Host ''
		Write-Host 'Next, set up your editor:'
		Write-Host "  $nextCommand"
	}
	Find-OtherNomi $dir $env:PATH $separator $replace
}

Install-Nomi
