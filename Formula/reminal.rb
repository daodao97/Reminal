class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.4"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.4/reminal_2.0.4_darwin_arm64.tar.gz"
      sha256 "cf1a0fd424022915039ca6293cdc9c2ba9269c9bb98d4fb72fa4eff9b4ae240c"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.4/reminal_2.0.4_darwin_amd64.tar.gz"
      sha256 "eb122e14f5b810657642ec90a2bfa2f2bc427667a14fea483bb49724ef12047a"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.4/reminal_2.0.4_linux_arm64.tar.gz"
      sha256 "18d426844b151e2e5bc2e5a806b4392bddcad6c9990f9ba108e2c25baa6faf3d"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.4/reminal_2.0.4_linux_amd64.tar.gz"
      sha256 "a8d643974d00c685f37b25296bf649ea6ae2315caebc6204f2462117d52c512f"
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
