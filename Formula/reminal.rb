class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.1"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.1/reminal_2.1.1_darwin_arm64.tar.gz"
      sha256 "c7a191448852b52bca982723b6b5c5f0b0752c2fff7f1815b1b27ca6d9af2a11"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.1/reminal_2.1.1_darwin_amd64.tar.gz"
      sha256 "7cf64c0a234cc6469dbc8ec3952c71919a3c89426df678d17506ea101d319479"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.1/reminal_2.1.1_linux_arm64.tar.gz"
      sha256 "97be1a7caa1d3da7b40722cbfdd1a127828bd0c6b1ae4b88b682723906177702"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.1/reminal_2.1.1_linux_amd64.tar.gz"
      sha256 "48d1596c1e3f07c9a8c7c5e8e8d6c1813a1784fc61e2c9d15ed119068ed02110"
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
