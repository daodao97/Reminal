class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.3"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.3/reminal_2.0.3_darwin_arm64.tar.gz"
      sha256 "a70fe909831927897e843133fb528ed35e328c92c58962ccf419979a90ab8ec4"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.3/reminal_2.0.3_darwin_amd64.tar.gz"
      sha256 "98ccfe63d40def9a351a16445b824a6f115d359fc2ab2d01e3805c5b99bfdd44"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.3/reminal_2.0.3_linux_arm64.tar.gz"
      sha256 "5c51b17bb92ce110af0cbeb450fcbcd9e0969fbd7d9d3ecaf38243069fe0836e"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.3/reminal_2.0.3_linux_amd64.tar.gz"
      sha256 "79430f0cb1c7c73d885ed23994c792004a1696e83e216f48a1f283d8180a61be"
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
