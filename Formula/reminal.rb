class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "3.0.3"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.3/reminal_3.0.3_darwin_arm64.tar.gz"
      sha256 "5ed7900082196a23c148cd7e3e013a10e446feef344a1f27bb336cf37fc717e1"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.3/reminal_3.0.3_darwin_amd64.tar.gz"
      sha256 "64455b11845c860e5e0dbcee8bccf65fe50fcd7efd24a4eabae81ba0a67dbb98"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.3/reminal_3.0.3_linux_arm64.tar.gz"
      sha256 "cf396ebb1271b92ada0854e91dd5b3538b8785f3d44ca6d9b86a9675b68c71b5"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.3/reminal_3.0.3_linux_amd64.tar.gz"
      sha256 "f8a24eb7dabc5eb2011a4d4e8f352b5f28d09e6edd58fd8af57213511db6741a"
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
