class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.0"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.0/reminal_2.0.0_darwin_arm64.tar.gz"
      sha256 "2d9132273221d217e3cf35354e8816da9c7a1b0a84f05e1de0f4a4ea366897c0"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.0/reminal_2.0.0_darwin_amd64.tar.gz"
      sha256 "0b19d0d73b641164bbe1c4d5211b9ec377df1ddbe7f4c226a6a753780460f546"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.0/reminal_2.0.0_linux_arm64.tar.gz"
      sha256 "de521e3beea577ccdcdc88da797accfce0cb02a6e9e830f9109834f21d226e40"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.0/reminal_2.0.0_linux_amd64.tar.gz"
      sha256 "8b14e4a57c55a6ee59dcc3fffc1d0682439a3f21f2cfd85520d38efa29c9aa13"
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
