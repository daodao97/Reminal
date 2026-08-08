class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.6"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.6/reminal_2.0.6_darwin_arm64.tar.gz"
      sha256 "b99414229c72a14cc7ad773020b49977c227c42932287db7c57a65c5e846e649"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.6/reminal_2.0.6_darwin_amd64.tar.gz"
      sha256 "d98270ac04bc18ec3e1f03e32cbbf9dd1c43f1face13013fec999b46bc4b83b9"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.6/reminal_2.0.6_linux_arm64.tar.gz"
      sha256 "024b1825e608ead89149a796d3d10dcabb6df33e3692a63df435b4b9398b12c7"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.6/reminal_2.0.6_linux_amd64.tar.gz"
      sha256 "ce3941e323242b2bc7af1bc0f891db6133b5a837650abd9c9dc3c776df1c6552"
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
