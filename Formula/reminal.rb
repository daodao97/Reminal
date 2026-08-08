class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.5"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.5/reminal_2.0.5_darwin_arm64.tar.gz"
      sha256 "5ba33b25b45cb9ac78ef29f4a8e67b0d1d74e76e464e408ebfef27f684f8af1c"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.5/reminal_2.0.5_darwin_amd64.tar.gz"
      sha256 "587434de1e239958fcae607ce7fab01e89007fcd0aef65024660e1950de5f957"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.5/reminal_2.0.5_linux_arm64.tar.gz"
      sha256 "cff452ae4fac76092bf553701a37795c4ea02a9e4d1586d4a6c71184d636f133"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.5/reminal_2.0.5_linux_amd64.tar.gz"
      sha256 "03605ea6d11c8d2c758645ee2f3d2f6ebb12c2fbb94bdbf8177d0e0ec72548f5"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
      # Build the native window-capture helper from source when Xcode is present;
      # otherwise the window mirror falls back to screencapture.
      if OS.mac? && which("swiftc")
        system "swiftc", "-O", "-o", bin/"reminal-capture", "native/reminal-capture/main.swift"
      end
    else
      bin.install "reminal"
      # The darwin bottle bundles the ScreenCaptureKit capture helper next to the
      # binary; the agent auto-discovers it for the native window mirror.
      bin.install "reminal-capture" if File.exist?("reminal-capture")
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
